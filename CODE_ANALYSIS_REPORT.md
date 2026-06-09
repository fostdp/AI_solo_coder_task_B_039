# LNG储罐翻滚预测系统 - 代码分析报告

## 报告日期
2026-06-10

---

## 一、Go后端服务技术实现

### 1.1 Modbus轮询调度

**核心文件**：[backend/modbus/client.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/modbus/client.go)

#### 1.1.1 三级优先级队列设计

```
PriorityHigh (3)   → 压力、BOG压缩机 (30秒周期, 重试3次)
PriorityMedium (2) → 密度计 (30秒周期, 重试2次)
PriorityLow (1)    → 温度传感器阵列 (60秒周期, 重试1次)
```

#### 1.1.2 二叉堆优先级队列实现

```go
// 堆结构定义
type PriorityQueue struct {
    tasks  []*ModbusTask
    mu     sync.Mutex
}

// Push: O(log n) 向上堆化
func (pq *PriorityQueue) Push(task *ModbusTask) {
    pq.tasks = append(pq.tasks, task)
    pq.heapifyUp(len(pq.tasks) - 1)
}

// Pop: O(log n) 向下堆化，弹出最高优先级任务
func (pq *PriorityQueue) Pop() *ModbusTask {
    task := pq.tasks[0]
    last := len(pq.tasks) - 1
    pq.tasks[0] = pq.tasks[last]
    pq.tasks = pq.tasks[:last]
    pq.heapifyDown(0)
    return task
}
```

**关键特性**：
- 高优先级任务始终先于低优先级执行
- 失败任务根据`MaxRetries`自动重试，1秒后重新入队
- 双Ticker调度：高/中优先级30秒，低优先级60秒
- 独立goroutine处理队列（`processQueue`）

#### 1.1.3 任务调度架构

```
┌─────────────────────┐ 30s  ┌─────────────────────┐
│ highFreq Ticker     │────▶│ enqueueHighPriority  │
└─────────────────────┘     └─────────────────────┘
                                      │
┌─────────────────────┐ 30s  ┌─────────────────────┐
│ (同Ticker复用)      │────▶│ enqueueMediumPriority│
└─────────────────────┘     └─────────────────────┘
                                      │
┌─────────────────────┐ 60s  ┌─────────────────────┐
│ lowFreq Ticker      │────▶│ enqueueLowPriority   │
└─────────────────────┘     └─────────────────────┘
                                      │
                                      ▼
                              ┌─────────────────────┐
                              │  二叉堆优先级队列   │
                              └─────────────────────┘
                                      │
                                      ▼
                              ┌─────────────────────┐
                              │ processQueue goroutine│
                              └─────────────────────┘
```

**单座储罐采集映射**：
| 地址偏移 | 类型 | 数量 | 优先级 |
|----------|------|------|--------|
| 0-39 | 温度传感器 (5×8) | 40 | Low |
| 500-505 | 密度计 | 3 | Medium |
| 600-601 | 压力变送器 | 1 | High |
| 700-718 | BOG压缩机 | 2 | High |

---

### 1.2 TimescaleDB批量写入

**核心文件**：[backend/database/database.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/database/database.go)

#### 1.2.1 连接池配置

```go
poolConfig.MaxConns = 20     // 最大连接数
poolConfig.MinConns = 5      // 最小空闲连接
```

#### 1.2.2 批量写入实现

使用`pgxpool.Batch`进行批量操作，减少网络往返：

```go
func (db *DB) InsertTemperatureData(ctx context.Context, data []models.TemperatureData) error {
    batch := &pgxpool.Batch{}
    for _, d := range data {
        batch.Queue(`INSERT INTO temperature_data 
            (time, tank_id, layer, sensor_index, temperature, modbus_address)
            VALUES ($1, $2, $3, $4, $5, $6)`,
            d.Time, d.TankID, d.Layer, d.SensorIndex, d.Temperature, d.ModbusAddress)
    }
    return db.pool.SendBatch(ctx, batch).Close()
}
```

#### 1.2.3 写入方法清单

| 方法 | 数据类型 | 批量大小 | 超表 |
|------|----------|----------|------|
| `InsertTemperatureData` | 温度 | 40条/罐 | temperature_data |
| `InsertDensityData` | 密度 | 3条/罐 | density_data |
| `InsertPressureData` | 压力 | 1条/罐 | pressure_data |
| `InsertBOGCompressorData` | BOG | 2条/罐 | bog_compressor_data |
| `InsertLayerSummary` | 层汇总 | 5条/罐 | layer_summary |

#### 1.2.4 查询模式

**最新数据查询**（使用`DISTINCT ON`优化）：
```sql
SELECT DISTINCT ON (layer, sensor_index) time, tank_id, layer, sensor_index, temperature
FROM temperature_data 
WHERE tank_id = $1 
ORDER BY layer, sensor_index, time DESC 
LIMIT $2
```

**趋势数据查询**：
```sql
SELECT time, temperature 
FROM temperature_data 
WHERE tank_id = $1 AND layer = $2 AND sensor_index = $3 AND time >= $4 
ORDER BY time
```

**层平均温度查询**（5分钟窗口）：
```sql
SELECT layer, AVG(temperature) as avg_temp
FROM temperature_data 
WHERE tank_id = $1 AND time >= NOW() - INTERVAL '5 minutes'
GROUP BY layer ORDER BY layer
```

---

### 1.3 OPC UA客户端实现

**核心文件**：[backend/alarm/opcua.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/alarm/opcua.go)

#### 1.3.1 架构设计

```
┌─────────────────────────────────────────────────────┐
│                OPCUAClient                          │
├─────────────────────────────────────────────────────┤
│  ┌──────────────┐    ┌────────────────────────┐    │
│  │ Heartbeat    │    │ Alarm Buffer (max 100) │    │
│  │ (10s ticker) │    │ 循环缓冲，FIFO策略     │    │
│  └──────┬───────┘    └──────────┬─────────────┘    │
│         │                       │                  │
│         ▼                       ▼                  │
│  ┌──────────────┐    ┌────────────────────────┐    │
│  │ checkConnection │  │ flushBufferedAlarms    │    │
│  │ (读取i=2258)  │    │ 连接恢复后自动补发     │    │
│  └──────┬───────┘    └──────────┬─────────────┘    │
│         │                       │                  │
│         ▼                       ▼                  │
│  ┌──────────────┐    ┌────────────────────────┐    │
│  │ tryReconnect │    │ PushAlarm (先写缓存)   │    │
│  │ 指数退避     │    │                        │    │
│  └──────────────┘    └────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

#### 1.3.2 心跳检测机制

**检测节点**：Server时间节点 `i=2258`（标准OPC UA节点）

```go
func (c *OPCUAClient) StartHeartbeat(ctx context.Context) {
    ticker := time.NewTicker(10 * time.Second)
    go func() {
        for {
            select {
            case <-ticker.C:
                if err := c.checkConnection(); err != nil {
                    go c.tryReconnect()  // 连接丢失，异步重连
                }
            }
        }
    }()
}
```

#### 1.3.3 指数退避重连策略

```go
func (c *OPCUAClient) tryReconnect() {
    maxAttempts := 10
    for attempt := 0; attempt < maxAttempts; attempt++ {
        delay := time.Duration(attempt+1) * 3 * time.Second
        if delay > 30*time.Second {
            delay = 30 * time.Second
        }
        time.Sleep(delay)
        
        if err := c.Connect(); err == nil {
            return  // 重连成功
        }
    }
}
```

**重连节流保护**：
```go
now := time.Now()
if now.Sub(c.lastConnectAttempt) < 5*time.Second && c.reconnectCount > 0 {
    delay := time.Duration(c.reconnectCount) * 2 * time.Second
    if now.Sub(c.lastConnectAttempt) < delay {
        return fmt.Errorf("reconnect throttled")
    }
}
```

#### 1.3.4 告警缓存与补发

```go
const maxBufferSize = 100

func (c *OPCUAClient) bufferAlarm(alarm *models.Alarm) {
    c.bufferMu.Lock()
    defer c.bufferMu.Unlock()
    if len(c.alarmBuffer) >= maxBufferSize {
        c.alarmBuffer = c.alarmBuffer[1:]  // 循环缓冲，丢弃最旧
    }
    c.alarmBuffer = append(c.alarmBuffer, alarm)
}

// 连接恢复后自动调用
func (c *OPCUAClient) flushBufferedAlarms() {
    for _, alarm := range alarms {
        if err := c.pushAlarmInternal(alarm); err != nil {
            c.bufferAlarm(alarm)  // 失败放回缓存
        }
    }
}
```

#### 1.3.5 告警数据结构

```go
alarmData := map[string]interface{}{
    "alarm_id":        alarm.AlarmID,
    "time":            alarm.Time.Format("2006-01-02 15:04:05"),
    "tank_id":         alarm.TankID,
    "alarm_level":     alarm.AlarmLevel,
    "alarm_type":      alarm.AlarmType,
    "alarm_message":   alarm.AlarmMessage,
    "threshold_value": alarm.ThresholdValue,
    "actual_value":    alarm.ActualValue,
}
```

---

## 二、翻滚预测模型数值方法

**核心文件**：[backend/prediction/rollover_model.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/prediction/rollover_model.go)

### 2.1 控制方程

#### 2.1.1 连续性方程（质量守恒）

```
∂ρ/∂t + ∂(ρu)/∂z = 0

其中:
  ρ - 密度场 (kg/m³)
  u - 垂直速度 (m/s)
  z - 垂直坐标 (m)
  t - 时间 (s)
```

#### 2.1.2 动量方程（Navier-Stokes简化）

```
∂u/∂t + u∂u/∂z = -g/ρ ∂ρ/∂z + ν ∂²u/∂z²

其中:
  g = 9.81 m/s²  - 重力加速度
  ν = 1e-6 m²/s  - 运动粘度
```

#### 2.1.3 输运方程（能量/密度）

```
∂ρ/∂t + u∂ρ/∂z = α_T ∂²ρ/∂z²

其中:
  α_T = 1e-7 m²/s  - 热扩散系数
```

### 2.2 方程离散化（有限体积法）

#### 2.2.1 计算网格

```
          z = H
          ┌───┐
          │ n │
          ├───┤  dz = H / (N-1)
          │ . │
          │ . │
          │ i │ ← 控制体中心
          │ . │
          │ . │
          ├───┤
          │ 0 │
          └───┘
          z = 0

N = cfg.GridPoints (默认50)
```

#### 2.2.2 空间离散（中心差分）

**一阶导数**：
```
∂ρ/∂z |_i ≈ (ρ[i+1] - ρ[i-1]) / (2*dz)
∂u/∂z |_i ≈ (u[i+1] - u[i-1]) / (2*dz)
```

**二阶导数**：
```
∂²ρ/∂z² |_i ≈ (ρ[i+1] - 2*ρ[i] + ρ[i-1]) / (dz*dz)
∂²u/∂z² |_i ≈ (u[i+1] - 2*u[i] + u[i-1]) / (dz*dz)
```

**对流项离散**：
```
u ∂ρ/∂z |_i ≈ u[i] * (ρ[i+1] - ρ[i-1]) / (2*dz)
```

**扩散项离散**：
```
α_T ∂²ρ/∂z² |_i ≈ α_T * (ρ[i+1] - 2*ρ[i] + ρ[i-1]) / (dz*dz)
```

#### 2.2.3 时间离散（显式欧拉）

带欠松弛因子的更新：
```go
// 速度更新
u[i] = uPrev[i] + underRelaxation * dt * (buoyancy + nu*du_dz2)

// 密度更新
rhoNew[i] = rho[i] + underRelaxation * dt * (advection + diffusion)
```

#### 2.2.4 欠松弛因子动态调整

```go
underRelaxation = 0.5      // 初始值
minRelaxation = 0.1
maxRelaxation = 0.8

// 发散时减小
if residual > maxResidual * 2.0 {
    underRelaxation = max(minRelaxation, underRelaxation * 0.6)
}

// 收敛时增大
if residual < maxResidual * 0.5 {
    underRelaxation = min(maxRelaxation, underRelaxation * 1.05)
}
```

#### 2.2.5 CFL稳定性条件

```
CFL = |u[i]| * dt / dz ≤ 0.5

// 动态调整时间步长
if CFL > 0.5 {
    dt = 0.4 * dz / max(|u[i]|, 1e-10)
    dt = clamp(dt, dtMin=0.001, dtMax=0.5)
}
```

### 2.3 自适应时间步长策略

```
┌─────────────────────────────────────────────────────┐
│                时间步长调整逻辑                     │
├─────────────────────────────────────────────────────┤
│  初始 dt = 0.1                                       │
│                                                     │
│  ▶ CFL > 0.5 → dt = 0.4 * dz / |u|                  │
│  ▶ 连续发散2次 → dt *= 0.5                          │
│  ▶ 残差减半 → dt *= 1.1                             │
│  ▶ 边界突变(>0.5) → dt *= 0.5                       │
│                                                     │
│  约束: 0.001 ≤ dt ≤ 0.5                             │
└─────────────────────────────────────────────────────┘
```

### 2.4 残差监控

**连续性方程残差**：
```go
contRes := (rhoNew[i]-rho[i])/dt + (u[i+1]*rhoNew[i+1] - u[i-1]*rhoNew[i-1])/(2*dz)
residual += contRes * contRes
residual = sqrt(residual / (n-2))  // L2范数
```

**发散检测与回退**：
```go
if residual > maxResidual * 2.0 && maxResidual > 1e-10 {
    consecutiveDivergence++
    if consecutiveDivergence >= 2 {
        underRelaxation *= 0.6
        dt *= 0.5
        copy(rho, rhoPrev)   // 回退到上一步
        copy(u, uPrev)
        continue             // 重新计算
    }
}
```

### 2.5 分层稳定性评估

#### 2.5.1 浮力频率（Brunt-Väisälä频率）

```
N² = -g/ρ * ∂ρ/∂z

若 N² > 0 → 稳定分层（重流体在下）
若 N² < 0 → 不稳定分层（可能发生翻滚）
```

#### 2.5.2 稳定性指数

```go
stability = 1 - exp(-N² * 100)

stability ∈ [0, 1]
  接近0 → 不稳定
  接近1 → 强稳定
```

#### 2.5.3 瑞利数（对流强度）

```
Ra = g * β * ΔT * H³ / (ν * α_T)

其中:
  β = 1/ρ * ∂ρ/∂T ≈ 0.001  (热膨胀系数)
  ΔT - 垂直温差
  H - 储罐高度
```

### 2.6 临界时间判定

#### 2.6.1 判据1：密度界面梯度突变

```go
interfaceHeight, gradient := p.findDensityInterface(rho, grid.Heights)
if interfaceHeight > 0 && gradient > 50 && criticalTime < 0 {
    criticalTime = float64(step) * dt / 3600.0  // 转换为小时
    break
}
```

#### 2.6.2 判据2：对流速度阈值

```go
if math.Abs(u[n/2]) > 0.01 && criticalTime < 0 {
    criticalTime = float64(step) * dt / 3600.0
    break
}
```

#### 2.6.3 风险指数加权计算

```go
riskIndex := 0.35*tempDiffScore +    // 温度差权重 35%
             0.25*densityDiffScore + // 密度差权重 25%
             0.25*instabilityScore + // 不稳定性权重 25%
             0.15*timeScore          // 临界时间权重 15%
```

---

## 三、前端可视化技术实现

### 3.1 三维储罐WebGL渲染

**核心文件**：[frontend/js/threeScene.js](file:///d:/SOLO-2/AI_solo_coder_task_A_039/frontend/js/threeScene.js)

#### 3.1.1 设备检测与质量分级

```javascript
detectDevice() {
    const ua = navigator.userAgent.toLowerCase();
    const isMobile = /android|webos|iphone|ipad|ipod|blackberry|iemobile|opera mini/i.test(ua);
    const isLowEnd = window.innerWidth < 768 || navigator.hardwareConcurrency <= 4;
    this.isMobile = isMobile || isLowEnd;
}
```

**分级配置表**：

| 参数 | 桌面端 | 移动端 | 降低幅度 |
|------|--------|--------|----------|
| cylinderSegments | 64 | 24 | -62.5% |
| wireframeSegments | 32 | 16 | -50% |
| sphereSegments | 16 | 12 | -25% |
| pixelRatio | 2 | 1 | -50% |
| shadows | true | false | - |
| fog | true | false | - |
| antialias | true | false | - |
| glowEffects | true | false | - |

#### 3.1.2 场景架构

```
Scene
├── TankGroup (userData.type='tank')
│   ├── OuterShell (CylinderGeometry, 半透明外壳)
│   ├── Wireframe (线框装饰)
│   ├── LayerMeshes[] (5个分层圆柱)
│   ├── RingMarkers[] (5个层级圆环)
│   ├── Base (储罐底座)
│   ├── Top (罐顶板)
│   ├── Dome (半球穹顶, 移动端隐藏)
│   └── SensorGroup
│       ├── TemperatureSensors (40个Sphere)
│       │   └── Glow (发光效果, 移动端隐藏)
│       └── DensitySensors (3个Cylinder)
│           └── Glow (发光效果, 移动端隐藏)
├── AmbientLight (环境光)
├── DirectionalLight (主光源, 带阴影)
├── PointLight1 (青色点光源, 移动端隐藏)
└── PointLight2 (蓝色点光源, 移动端隐藏)
```

#### 3.1.3 渲染性能优化

**像素比控制**：
```javascript
this.renderer.setPixelRatio(this.quality.pixelRatio);
```

**阴影控制**：
```javascript
this.renderer.shadowMap.enabled = this.quality.shadows;
if (this.quality.shadows) {
    directionalLight.shadow.mapSize.width = 1024;  // 从2048降至1024
}
```

**动画降级**：
```javascript
if (this.quality.animationQuality === 'high') {
    // 桌面端：正弦脉冲动画
    const pulse = 1 + Math.sin(time * 2 + index * 0.5) * 0.1;
    sensor.scale.setScalar(baseScale * pulse);
} else {
    // 移动端：仅缩放，无动画
    sensor.scale.setScalar(baseScale);
}
```

#### 3.1.4 FPS监控与动态降级

```javascript
updateFPS() {
    // 每秒计算平均FPS
    const avgFPS = this.fpsHistory.reduce((a,b)=>a+b,0) / this.fpsHistory.length;
    
    if (this.isMobile && avgFPS < 20) {
        this.downgradeQuality();  // 自动降级
    }
}

downgradeQuality() {
    // 高质量 → 中等质量
    if (this.quality.animationQuality === 'high') {
        this.quality.animationQuality = 'medium';
        this.quality.cylinderSegments -= 8;
    }
    // 中等质量 → 低质量
    else if (this.quality.animationQuality === 'medium') {
        this.quality.animationQuality = 'low';
        this.quality.shadows = false;
        this.quality.antialias = false;
        this.renderer.shadowMap.enabled = false;
    }
}
```

#### 3.1.5 传感器交互

**Raycaster拾取**：
```javascript
this.raycaster.setFromCamera(this.mouse, this.camera);
const intersects = this.raycaster.intersectObjects(this.sensors);

if (intersects.length > 0) {
    const sensor = intersects[0].object;
    if (this.onSensorClick) {
        this.onSensorClick(sensor.userData);
    }
}
```

---

### 3.2 等值线Canvas绘制

**核心文件**：[frontend/js/visualization.js](file:///d:/SOLO-2/AI_solo_coder_task_A_039/frontend/js/visualization.js#L46-L157)

#### 3.2.1 绘制流程

```
┌─────────────────────────────────────────────────────┐
│              等值线绘制流程                          │
├─────────────────────────────────────────────────────┤
│  1. createDensityGrid()                              │
│     - 50×50网格                                      │
│     - 线性插值密度场                                 │
│                                                     │
│  2. marchingSquares(grid, level) × 5级               │
│     - 遍历每个网格单元                               │
│     - 查找等值点                                     │
│     - 查表生成线段                                   │
│                                                     │
│  3. drawContourLines()                               │
│     - 极坐标转换（柱面展开）                         │
│     - 按层级设置线宽和透明度                         │
└─────────────────────────────────────────────────────┘
```

#### 3.2.2 密度网格生成

```javascript
createDensityGrid(densityData, densityHeights, layerHeights) {
    const gridSize = 50;
    const grid = [];
    
    for (let y = 0; y < gridSize; y++) {
        grid[y] = [];
        const h = (y / (gridSize - 1)) * CONFIG.TANK_HEIGHT;
        for (let x = 0; x < gridSize; x++) {
            grid[y][x] = this.interpolateDensity(densityData, densityHeights, h);
        }
    }
    return grid;
}
```

#### 3.2.3 Marching Squares算法

**16种等值线模式**：

```
    d ┌───┐ c        索引计算:
      │   │          idx = (a>level?1:0) | (b>level?2:0)
    a └───┘ b              | (c>level?4:0) | (d>level?8:0)
```

```javascript
const edgePoints = {
    1: [[x + t1, y]],
    2: [[x + 1, y + t2]],
    3: [[x + t1, y], [x + 1, y + t2]],
    4: [[x + 1 - t2, y + 1]],
    5: [[x + t1, y], [x + 1 - t2, y + 1]],
    6: [[x + 1, y + t2], [x + 1 - t2, y + 1]],
    7: [[x + t1, y], [x + 1, y + t2], [x + 1 - t2, y + 1]],
    8: [[x, y + t4]],
    9: [[x + t1, y], [x, y + t4]],
    10: [[x + 1, y + t2], [x, y + t4]],
    11: [[x + t1, y], [x + 1, y + t2], [x, y + t4]],
    12: [[x + 1 - t2, y + 1], [x, y + t4]],
    13: [[x + t1, y], [x + 1 - t2, y + 1], [x, y + t4]],
    14: [[x + 1, y + t2], [x + 1 - t2, y + 1], [x, y + t4]]
};
```

**线性插值求交点**：
```javascript
const t1 = (level - a) / (b - a);  // 边ab上的交点
const t2 = (level - b) / (c - b);  // 边bc上的交点
const t3 = (level - d) / (c - d);  // 边dc上的交点
const t4 = (level - a) / (d - a);  // 边ad上的交点
```

#### 3.2.4 柱面坐标转换

```javascript
// 网格坐标 → 柱面坐标 → 屏幕坐标
const [gx, gy] = contourPoints[i];
const angle = (gx / grid[0].length) * Math.PI * 2;  // 方位角
const h = 1 - (gy / grid.length);                     // 高度比例
const r = radius * h;                                 // 半径
const x = centerX + Math.cos(angle) * r;
const y = centerY - Math.sin(angle) * r * 0.8;        // 0.8为透视压缩
```

#### 3.2.5 等值线分级样式

| 层级 | 透明度 | 线宽 |
|------|--------|------|
| 0 | 0.4 | 1.0 |
| 1 | 0.52 | 1.4 |
| 2 | 0.64 | 1.8 |
| 3 | 0.76 | 2.2 |
| 4 | 0.88 | 2.6 |
| 5 | 1.0 | 3.0 |

---

### 3.3 风险指数仪表盘

**核心文件**：[frontend/js/visualization.js](file:///d:/SOLO-2/AI_solo_coder_task_A_039/frontend/js/visualization.js#L302-L328)

#### 3.3.1 SVG仪表盘结构

```svg
<svg id="risk-gauge" viewBox="0 0 120 110">
  <!-- 背景弧 -->
  <path d="M 15 55 A 45 45 0 0 1 105 55" 
        stroke="#333" stroke-width="8" fill="none"/>
  <!-- 风险弧 (动态) -->
  <path id="risk-arc" d="M 15 55 A 45 45 0 0 1 105 55" 
        stroke="url(#riskGradient)" stroke-width="8" fill="none"
        stroke-dasharray="0 158"/>
  <!-- 指针 -->
  <circle id="risk-pointer" cx="60" cy="55" r="5" fill="#fff"/>
  <!-- 数值 -->
  <text id="risk-value" x="60" y="75">0.0%</text>
</svg>
```

#### 3.3.2 弧长计算

```javascript
// 半圆周长 = π * r = 3.1416 * 45 ≈ 141.4
// 实际path长度（通过测量）= 158
const maxDash = 158;
const dashOffset = maxDash * (1 - riskIndex);
arc.style.strokeDasharray = `${maxDash - dashOffset} ${maxDash}`;
```

#### 3.3.3 指针角度计算

```javascript
const angle = Math.PI * riskIndex;  // 0 → π 弧度
const pointerX = 60 + 45 * Math.cos(angle);
const pointerY = 55 - 45 * Math.sin(angle);
pointer.setAttribute('cx', pointerX);
pointer.setAttribute('cy', pointerY);
```

#### 3.3.4 颜色分级

```javascript
let color;
if (riskIndex >= CONFIG.RISK_THRESHOLDS.HIGH) {      // >= 0.8
    color = '#ff0000';    // 红色 - 高风险
} else if (riskIndex >= CONFIG.RISK_THRESHOLDS.MEDIUM) {  // >= 0.6
    color = '#ffff00';    // 黄色 - 中风险
} else {
    color = '#00ff00';    // 绿色 - 低风险/安全
}
value.style.color = color;
```

#### 3.3.5 风险阈值配置

```javascript
RISK_THRESHOLDS: {
    HIGH: 0.8,      // 80%以上 - 高风险
    MEDIUM: 0.6,    // 60-80% - 中风险
    LOW: 0.2        // 20-60% - 低风险
}                   // 20%以下 - 安全
```

---

## 四、告警系统实现

**核心文件**：[backend/alarm/engine.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/alarm/engine.go)

### 4.1 系统架构

```
┌─────────────────────────────────────────────────────┐
│                   AlarmEngine                        │
├─────────────────────────────────────────────────────┤
│  定时器 (30秒)                                       │
│       │                                              │
│       ▼                                              │
│  CheckAndTriggerAlarms (每座储罐)                    │
│       ├─► checkRolloverAlarm                         │
│       └─► checkOverpressureAlarm                     │
│                                                          │
│  PushUnsentAlarms                                    │
│       └─► OPC UA推送 + DB标记                         │
└─────────────────────────────────────────────────────┘
```

### 4.2 两级分级逻辑

#### 4.2.1 一级翻滚预警

**触发条件**（同时满足）：
```go
maxTempDiff > cfg.TempDiffThreshold      // > 8℃
AND
maxDensityDiff > cfg.DensityDiffThreshold  // > 2 kg/m³
```

**计算逻辑**：
```go
// 最大层间温差
maxTempDiff := 0.0
for i := 1; i < len(layerTemps); i++ {
    diff := layerTemps[i] - layerTemps[i-1]
    if diff > maxTempDiff {
        maxTempDiff = diff
    }
}

// 最大密度差（上层-下层，正值表示上层密度大，不稳定）
maxDensityDiff := 0.0
for i := 1; i < len(densityData); i++ {
    diff := densityData[i-1].Density - densityData[i].Density
    if diff > maxDensityDiff {
        maxDensityDiff = diff
    }
}
```

**告警信息**：
```
{储罐名}储罐一级翻滚预警：
层间温差{maxTempDiff}℃超过阈值{TempDiffThreshold}℃，
密度差{maxDensityDiff}kg/m³超过阈值{DensityDiffThreshold}kg/m³。
建议立即开启低压泵循环混合。
```

#### 4.2.2 二级超压告警

**触发条件**：
```go
pressurePct = (pressure / tank.DesignPressure) * 100.0
pressurePct > cfg.PressureThresholdPct  // > 90%
```

**告警信息**：
```
{储罐名}储罐二级超压告警：
当前压力{pressure}MPa达到设计压力{DesignPressure}MPa的{pressurePct}%，
超过阈值{PressureThresholdPct}%。
请立即检查BOG压缩机运行状态！
```

#### 4.2.3 告警去重机制

```go
// 查询是否已有未清除的同类型告警
activeAlarms, _ := e.db.GetActiveAlarms(ctx)
hasActiveRolloverAlarm := false
for _, a := range activeAlarms {
    if a.TankID == tankID && a.AlarmType == "ROLLOVER_WARNING" && !a.Cleared {
        hasActiveRolloverAlarm = true
        break
    }
}

// 仅在无活动告警时才触发新告警
if !hasActiveRolloverAlarm {
    alarmID, _ := e.db.InsertAlarm(ctx, alarm)
}
```

#### 4.2.4 告警自动清除

```go
// 当条件不满足且有活动告警时，自动清除
if maxTempDiff <= e.cfg.TempDiffThreshold || maxDensityDiff <= e.cfg.DensityDiffThreshold {
    if hasActiveRolloverAlarm {
        for _, a := range activeAlarms {
            if a.TankID == tankID && a.AlarmType == "ROLLOVER_WARNING" && !a.Cleared {
                e.db.ClearAlarm(ctx, a.AlarmID)
            }
        }
    }
}
```

### 4.3 OPC UA推送

**推送流程**：
```go
func (e *AlarmEngine) PushUnsentAlarms(ctx context.Context) error {
    alarms, _ := e.db.GetActiveAlarms(ctx)
    
    for _, alarm := range alarms {
        if !alarm.OPCUAPushed {
            if err := e.opcuaClient.PushAlarm(&alarm); err != nil {
                continue  // 失败不标记，下次重试
            }
            e.db.MarkAlarmPushed(ctx, alarm.AlarmID)  // 标记已推送
        }
    }
    return nil
}
```

**推送数据结构**（映射到DCS）：
| 字段 | 类型 | DCS映射 |
|------|------|---------|
| alarm_id | int | 告警ID |
| time | string | 触发时间 |
| tank_id | int | 储罐编号 |
| alarm_level | int | 告警级别 (1/2) |
| alarm_type | string | 告警类型 |
| alarm_message | string | 告警文本 |
| threshold_value | float64 | 阈值 |
| actual_value | float64 | 实际值 |

### 4.4 BOG压缩机控制模拟

**当前实现**：
```go
func (e *AlarmEngine) triggerBOGCompressorAdjustment(ctx context.Context, tankID int) error {
    fmt.Printf("BOG压缩机自动调节已启动 - 储罐ID: %d\n", tankID)
    return nil
}
```

**设计目标功能**（待实现）：
1. 启动备用BOG压缩机（若未运行）
2. 提高运行压缩机的负载（Modbus写寄存器）
3. 开启低压泵循环系统（Modbus写控制寄存器）
4. 写入控制指令反馈到DCS

**控制逻辑**：
```
翻滚预警触发 → 开启低压泵循环（3台泵）
超压告警触发 → 启动所有BOG压缩机（100%负载）
           → 若压力持续上升 → 开启火炬放空
```

---

## 五、技术债清单（共10条）

### 🚨 技术债 #1：BOG压缩机控制为空实现

**文件**：[backend/alarm/engine.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/alarm/engine.go#L226-L229)

**问题描述**：
`triggerBOGCompressorAdjustment`方法仅打印日志，未实现真实的Modbus控制指令写入。告警触发后无法自动执行工艺调节，需人工操作。

**风险等级**：高
**影响范围**：安全联锁功能
**建议修复成本**：5人天
**修复建议**：
```go
// 补充Modbus写控制逻辑
func (e *AlarmEngine) triggerBOGCompressorAdjustment(ctx context.Context, tankID int) error {
    // 1. 启动备用压缩机
    startAddr := (tankID - 1) * 1000 + 700
    for comp := 1; comp <= 2; comp++ {
        statusAddr := startAddr + (comp - 1) * 10
        if _, err := e.modbusClient.WriteSingleRegister(statusAddr, 1); err != nil {
            return err
        }
    }
    // 2. 开启低压泵循环
    pumpAddr := (tankID - 1) * 1000 + 800
    for pump := 1; pump <= 3; pump++ {
        if _, err := e.modbusClient.WriteSingleRegister(pumpAddr + pump, 1); err != nil {
            return err
        }
    }
    return nil
}
```

---

### 🚨 技术债 #2：TimescaleDB未使用COPY批量插入

**文件**：[backend/database/database.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/database/database.go#L52-L60)

**问题描述**：
当前使用`pgxpool.Batch`逐条INSERT，未使用PostgreSQL的`COPY FROM`命令。4座储罐每轮询周期产生(40+3+1+2+5)×4=204条INSERT，性能比COPY低3-5倍。

**风险等级**：中
**影响范围**：数据写入性能
**建议修复成本**：3人天
**修复建议**：
```go
func (db *DB) InsertTemperatureDataCopy(ctx context.Context, data []models.TemperatureData) error {
    conn, err := db.pool.Acquire(ctx)
    if err != nil {
        return err
    }
    defer conn.Release()

    rows := pgx.CopyFromRows(...)
    _, err = conn.CopyFrom(ctx, pgx.Identifier{"temperature_data"},
        []string{"time", "tank_id", "layer", "sensor_index", "temperature", "modbus_address"},
        rows)
    return err
}
```

**预期性能提升**：写入吞吐量提升300-500%

---

### 🚨 技术债 #3：OPC UA告警推送缺少DCS确认机制

**文件**：[backend/alarm/opcua.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/alarm/opcua.go#L257-L276)

**问题描述**：
`PushAlarm`仅单向写入OPC UA节点，未等待DCS系统的ACK确认。DCS可能因网络问题未收到告警，但系统已标记为`opcua_pushed=true`。

**风险等级**：高
**影响范围**：告警可靠性
**建议修复成本**：4人天
**修复建议**：
1. DCS配置ACK反馈节点（`ns=2;s=AlarmAck`）
2. 推送后监听ACK节点变化
3. 超时未收到ACK则重发
4. 达到最大重试次数后升级通知

---

### 🚨 技术债 #4：前端Mock数据逻辑冗余分散

**文件**：[frontend/js/main.js](file:///d:/SOLO-2/AI_solo_coder_task_A_039/frontend/js/main.js#L139-L155, L353-L363, L400-L409)

**问题描述**：
多处try-catch降级逻辑分散：
- `useMockData()` - 储罐数据降级（139-155行）
- `renderMockTemperatureChart()` - 温度图表降级（608-618行）
- `renderMockDensityChart()` - 密度图表降级（620-630行）
- `renderMockRiskChart()` - 风险图表降级（632-634行）
- `updatePredictionPanel()` catch块 - 预测数据降级（253-255行）

**风险等级**：低
**影响范围**：代码可维护性
**建议修复成本**：2人天
**修复建议**：
```javascript
// 统一Mock数据服务
const MockService = {
    getTank3DData(tankId) { ... },
    getSensorTrend() { ... },
    getDensityTrend() { ... },
    getPrediction() { ... },
    isMockMode() { return this.mockMode; }
};

// API调用统一降级
async getTank3DData(tankId) {
    try {
        const res = await fetch(...);
        return await res.json();
    } catch (e) {
        console.warn('Using mock data for tank', tankId);
        return MockService.getTank3DData(tankId);
    }
}
```

---

### 🚨 技术债 #5：预测模型物理参数未校准

**文件**：[backend/prediction/rollover_model.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/prediction/rollover_model.go#L254-L257)

**问题描述**：
物理参数为硬编码的理论值，未使用真实LNG物性数据校准：
```go
g := 9.81          // 重力加速度（正确）
nu := 1.0e-6       // 运动粘度（水的参数，LNG应为~1.5e-7）
alphaT := 1.0e-7   // 热扩散系数（需要校准）
```

LNG实际物性参数（1atm, -162℃）：
- 密度：420-470 kg/m³
- 运动粘度：~1.5 × 10⁻⁷ m²/s
- 热导率：~0.13 W/(m·K)
- 比热容：~3.5 kJ/(kg·K)
- 热膨胀系数：~0.001 K⁻¹

**风险等级**：中
**影响范围**：预测准确性
**建议修复成本**：8人天
**修复建议**：
1. 增加物性参数配置文件
2. 引入温度/压力相关的物性计算
3. 使用历史数据进行模型参数辨识（最小二乘法）
4. 建立模型预测精度监控机制

---

### 🚨 技术债 #6：缺少单元测试和集成测试

**覆盖情况**：代码测试覆盖率 ≈ 0%

**核心模块待测试**：
| 模块 | 测试类型 | 建议用例数 |
|------|----------|------------|
| PriorityQueue | 单元 | 15 |
| RolloverPredictor | 单元 | 20 |
| AlarmEngine | 单元 | 12 |
| OPCUAClient | 单元 + Mock | 10 |
| Modbus Collector | 集成 + Mock | 8 |
| Database | 集成 | 15 |
| API Endpoints | E2E | 10 |

**风险等级**：高
**影响范围**：代码质量、回归风险
**建议修复成本**：15人天
**修复建议**：
```go
// 使用testify框架示例
func TestPriorityQueue(t *testing.T) {
    pq := &PriorityQueue{}
    pq.Push(&ModbusTask{ID: "low", Priority: PriorityLow})
    pq.Push(&ModbusTask{ID: "high", Priority: PriorityHigh})
    
    task := pq.Pop()
    assert.Equal(t, "high", task.ID)  // 高优先级先出
}
```

---

### 🚨 技术债 #7：缺少集中式日志和监控

**文件**：所有Go文件使用`fmt.Printf`

**问题描述**：
- 日志使用`fmt.Printf`分散打印，无结构化
- 缺少日志级别（DEBUG/INFO/WARN/ERROR）
- 缺少请求ID追踪
- 缺少性能指标采集（采集延迟、预测耗时、写入耗时）
- 缺少告警指标统计（触发次数、推送成功率）

**风险等级**：中
**影响范围**：运维可观测性
**建议修复成本**：6人天
**修复建议**：
```go
// 引入Zap日志库
import "go.uber.org/zap"

logger.Info("Modbus collection completed",
    zap.Int("tank_id", tankID),
    zap.Duration("duration", elapsed),
    zap.Int("points_count", len(tempData)),
    zap.String("status", "success"),
)

// 引入Prometheus指标
var (
    modbusCollectDuration = prometheus.NewHistogram(...)
    alarmPushTotal = prometheus.NewCounter(...)
)
```

---

### 🚨 技术债 #8：API缺少限流和认证

**文件**：[backend/api/server.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/api/server.go)

**问题描述**：
- API接口完全开放，无任何认证
- 无请求限流，易受DoS攻击
- 敏感操作（告警确认/清除）无权限控制
- 无请求审计日志

**风险等级**：高
**影响范围**：系统安全
**建议修复成本**：7人天
**修复建议**：
```go
// JWT认证中间件
func AuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("Authorization")
        if !validateJWT(token) {
            c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized"})
            return
        }
        c.Next()
    }
}

// 限流中间件
func RateLimitMiddleware() gin.HandlerFunc {
    limiter := rate.NewLimiter(rate.Limit(10), 20)  // 10/s, 突发20
    return func(c *gin.Context) {
        if !limiter.Allow() {
            c.AbortWithStatusJSON(429, gin.H{"error": "Too Many Requests"})
            return
        }
        c.Next()
    }
}
```

---

### 🚨 技术债 #9：前端ECharts图表缺少防抖和销毁

**文件**：[frontend/js/main.js](file:///d:/SOLO-2/AI_solo_coder_task_A_039/frontend/js/main.js#L414-L606)

**问题描述**：
- 模态框频繁打开/关闭时，ECharts实例未销毁，可能内存泄漏
- 传感器快速点击时，图表重复初始化
- 窗口resize事件未防抖，可能频繁重绘

**风险等级**：低
**影响范围**：前端性能
**建议修复成本**：2人天
**修复建议**：
```javascript
// 防抖包装
function debounce(fn, delay) {
    let timer = null;
    return function(...args) {
        clearTimeout(timer);
        timer = setTimeout(() => fn.apply(this, args), delay);
    };
}

// 模态框关闭时销毁图表
function closeModal() {
    if (window.app.tempChart) {
        window.app.tempChart.dispose();
        window.app.tempChart = null;
    }
    // ... 销毁其他图表
    document.getElementById('sensor-modal').style.display = 'none';
}
```

---

### 🚨 技术债 #10：配置缺少热加载和校验

**文件**：[backend/config/config.go](file:///d:/SOLO-2/AI_solo_coder_task_A_039/backend/config/config.go)

**问题描述**：
- 配置加载后无法动态更新（告警阈值修改需重启）
- 配置值缺少范围校验（如端口0-65535）
- 缺少配置变更审计日志

**风险等级**：中
**影响范围**：运维灵活性
**建议修复成本**：4人天
**修复建议**：
```go
// 使用viper热加载
import "github.com/spf13/viper"

viper.WatchConfig()
viper.OnConfigChange(func(e fsnotify.Event) {
    log.Info("Config changed", zap.String("file", e.Name))
    // 重新加载配置
})

// 配置校验
func (c *Config) Validate() error {
    if c.Modbus.Port < 1 || c.Modbus.Port > 65535 {
        return fmt.Errorf("invalid modbus port: %d", c.Modbus.Port)
    }
    if c.Alarm.TempDiffThreshold <= 0 {
        return fmt.Errorf("temp threshold must be positive")
    }
    return nil
}
```

---

## 六、技术债优先级排序

| 优先级 | 技术债编号 | 风险等级 | 修复成本 | 修复成本/收益比 |
|--------|------------|----------|----------|------------------|
| 1 | #1 BOG控制空实现 | 高 | 5 | 高 |
| 2 | #6 缺少测试 | 高 | 15 | 中 |
| 3 | #8 API安全 | 高 | 7 | 高 |
| 4 | #3 OPC UA ACK | 高 | 4 | 高 |
| 5 | #5 模型参数 | 中 | 8 | 中 |
| 6 | #7 日志监控 | 中 | 6 | 高 |
| 7 | #2 COPY批量写入 | 中 | 3 | 高 |
| 8 | #10 配置热加载 | 中 | 4 | 中 |
| 9 | #4 Mock逻辑冗余 | 低 | 2 | 中 |
| 10 | #9 图表防抖 | 低 | 2 | 低 |

---

## 七、总结

### 代码质量评估

| 维度 | 评分 (1-10) | 说明 |
|------|-------------|------|
| 架构设计 | 8 | 模块化清晰，职责分离合理 |
| 性能优化 | 7 | 已做关键优化，批量写入可进一步提升 |
| 错误处理 | 6 | 错误捕获较完善，但缺少重试策略 |
| 可测试性 | 3 | 无单元测试，部分模块耦合较紧 |
| 可维护性 | 6 | 命名规范，但文档不足 |
| 安全性 | 4 | 缺少认证、限流 |
| 可观测性 | 3 | 无结构化日志和监控指标 |

**总体评分**：5.9/10

### 主要优势
1. ✅ 数值方法专业（有限体积法+稳定性控制）
2. ✅ Modbus优先级队列设计合理
3. ✅ OPC UA重连和缓存机制完善
4. ✅ 前端可视化交互流畅
5. ✅ 移动端性能优化到位

### 建议改进顺序
1. **立即修复**：#1 BOG控制空实现（安全联锁）
2. **短期修复**：#8 API安全、#3 OPC UA ACK
3. **中期修复**：#6 测试、#7 日志监控、#2 批量写入
4. **长期优化**：#5 模型校准、#10 配置热加载

---

## 报告结束
