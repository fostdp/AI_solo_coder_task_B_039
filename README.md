# LNG储罐翻滚预测与安全监控系统

## 系统概述

本系统针对大型液化天然气（LNG）接收站的4座16万立方米储罐，实现翻滚（Rollover）现象的实时预测与安全监控。系统通过Modbus TCP采集罐内温度、密度、压力等数据，采用有限体积法求解分层对流方程，预测分层失稳临界时间，当翻滚风险超过阈值时通过OPC UA推送告警至DCS系统。

## 系统架构

```
                          ┌──────────────────────────────┐
                          │        前端Web界面           │
                          │  (Nginx + Gzip压缩)         │
                          │  ┌─────────────────────┐     │
                          │  │ tank_3d_viewer.js   │     │
                          │  │ risk_dashboard.js   │     │
                          │  └─────────────────────┘     │
                          └──────────────┬───────────────┘
                                         │ HTTP/REST API
                                         ▼
┌───────────────────────────────────────────────────────────────────────────┐
│                              Go后端服务                                   │
│  ┌─────────────────┐  Channel  ┌──────────────────┐  Channel  ┌─────────┐│
│  │  modbus_poller  │──────────▶│ rollover_predictor│──────────▶│  alarm  ││
│  │  (优先级队列)   │ PollResult │ (有限体积法)      │ Prediction │ forward ││
│  │                 │           │ (风险计算)        │ Result     │ (OPC UA)││
│  └────────┬────────┘           └────────┬─────────┘           └────┬────┘│
│           │                             │                            │     │
│           │ Modbus TCP                  │ 写入TimescaleDB           │ OPC UA│
└───────────┼─────────────────────────────┼────────────────────────────┼─────┘
            │                             │                            │
            ▼                             ▼                            ▼
┌──────────────────────┐      ┌──────────────────────┐      ┌─────────────────┐
│  Modbus模拟器        │      │   TimescaleDB        │      │  OPC UA模拟器   │
│  (4座×43传感器)      │      │ (自动压缩+保留策略)  │      │  (DCS模拟)      │
│  HTTP API控制        │      │  连续聚合视图        │      │  HTTP API控制   │
└──────────────────────┘      └──────────────────────┘      └─────────────────┘

┌───────────────────────────────────────────────────────────────────────────┐
│                              监控体系                                    │
│  ┌─────────────────┐          ┌──────────────────┐                        │
│  │   Prometheus    │◀─────────│   Go pprof       │                        │
│  │   (指标采集)     │  /metrics │  (性能分析)      │                        │
│  └─────────────────┘          └──────────────────┘                        │
└───────────────────────────────────────────────────────────────────────────┘
```

### 核心模块说明

| 模块 | 目录 | 职责 |
|------|------|------|
| **modbus_poller** | `backend/modbus_poller/` | Modbus数据采集、三级优先级队列调度（二叉堆实现）、层统计计算 |
| **rollover_predictor** | `backend/rollover_predictor/` | 有限体积法求解分层对流方程、欠松弛因子、自适应时间步长、残差监控、风险计算 |
| **alarm_forwarder** | `backend/alarm_forwarder/` | OPC UA告警推送、断线重连+心跳、BOG压缩机控制、低压泵控制 |
| **messages** | `backend/messages/` | 模块间channel通信消息定义 |
| **Tank3DViewer** | `frontend/js/tank_3d_viewer.js` | Three.js三维储罐可视化、WebGL性能优化、移动端适配 |
| **RiskDashboard** | `frontend/js/risk_dashboard.js` | 风险仪表盘、ECharts趋势图、告警管理、传感器详情 |

## 快速部署

### 环境要求
- Docker >= 24.0
- Docker Compose >= 2.20
- 内存 >= 4GB
- 磁盘 >= 20GB

### 一键启动

```bash
# 克隆项目
git clone <repository-url>
cd AI_solo_coder_task_A_039

# 启动所有服务
docker-compose up -d

# 查看服务状态
docker-compose ps

# 查看日志
docker-compose logs -f backend
```

### 服务访问地址

| 服务 | 地址 | 说明 |
|------|------|------|
| 前端监控界面 | http://localhost:80 | 三维视图+仪表盘 |
| Go后端API | http://localhost:8080 | REST API接口 |
| Prometheus监控 | http://localhost:9090 | 指标查询 |
| pprof性能分析 | http://localhost:6060/debug/pprof/ | Go性能分析 |
| Modbus模拟器API | http://localhost:8000 | 模拟器控制 |
| OPC UA模拟器API | http://localhost:8001 | DCS模拟控制 |
| Modbus TCP | localhost:5020 | 工业协议端口 |
| OPC UA | opc.tcp://localhost:4840 | 工业协议端口 |

### 停止服务

```bash
# 停止服务
docker-compose down

# 停止并清除数据（谨慎使用）
docker-compose down -v
```

## Modbus模拟器使用

### 功能特性
- 4座储罐，每座43个传感器（5层×8温度 + 3密度）
- 30秒数据更新间隔（可配置）
- 支持注入温度密度分层条件
- 支持手动触发翻滚事件
- HTTP API远程控制

### API接口

```bash
# 获取所有储罐状态
curl http://localhost:8000/api/status

# 健康检查
curl http://localhost:8000/health

# 触发翻滚事件（储罐ID: 1-4）
curl "http://localhost:8000/api/induce_rollover?tank_id=1"

# 注入分层条件
curl "http://localhost:8000/api/inject_stratification?tank_id=1&temp_diff=10.0&density_diff=3.0"

# 重置储罐数据
curl "http://localhost:8000/api/reset_tank?tank_id=1"

# 设置更新间隔（秒）
curl "http://localhost:8000/api/set_interval?seconds=15"
```

### 环境变量配置

```bash
MODBUS_HOST=0.0.0.0      # 监听地址
MODBUS_PORT=502          # Modbus端口
API_PORT=8000            # API端口
UPDATE_INTERVAL=30       # 数据更新间隔（秒）
```

## OPC UA模拟器使用

### 功能特性
- 模拟DCS系统OPC UA服务器
- 4座储罐节点（风险、温差、密度差、压力）
- 告警节点展示
- 系统状态监控
- HTTP API查询和管理

### 节点结构

```
Root
└─ Objects
   └─ LNGSystem (ns=2)
      ├─ Tanks
      │  ├─ Tank_1
      │  │  ├─ RolloverRisk (Float)
      │  │  ├─ TemperatureDiff (Float)
      │  │  ├─ DensityDiff (Float)
      │  │  └─ Pressure (Float)
      │  ├─ Tank_2 ... Tank_4
      ├─ Alarms
      │  ├─ Tank_1_Alarm (String)
      │  └─ ... Tank_4_Alarm
      ├─ SystemStatus (String)
      └─ ActiveAlarmsCount (Int32)
```

### API接口

```bash
# 获取服务器状态
curl http://localhost:8001/api/status

# 获取告警列表（最近100条）
curl http://localhost:8001/api/alarms

# 清除指定告警
curl "http://localhost:8001/api/clear_alarm?alarm_id=1"
```

## 监控与运维

### Prometheus指标

系统暴露以下Prometheus指标（`http://localhost:8080/metrics`）：

| 指标名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `modbus_poll_total` | Counter | tank_id, data_type | Modbus采集总次数 |
| `prediction_duration_seconds` | Histogram | tank_id | 预测计算耗时 |
| `alarm_total` | Counter | tank_id, alarm_level | 告警产生总数 |
| `active_connections` | Gauge | module | 活跃连接数 |

### pprof性能分析

```bash
# 采集30秒CPU profile
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# 查看内存使用
go tool pprof http://localhost:6060/debug/pprof/heap

# 查看goroutine
go tool pprof http://localhost:6060/debug/pprof/goroutine

# 查看阻塞操作
go tool pprof http://localhost:6060/debug/pprof/block
```

### TimescaleDB数据管理

自动配置的数据保留策略：

| 数据表 | 压缩时间 | 保留时间 |
|--------|----------|----------|
| temperature_data | 7天 | 3个月 |
| density_data | 7天 | 3个月 |
| pressure_data | 7天 | 3个月 |
| bog_compressor_data | 7天 | 3个月 |
| layer_summary | 7天 | 6个月 |
| rollover_prediction | 3天 | 1年 |
| alarms | - | 1年 |

连续聚合视图：
- `temperature_15min`：15分钟温度汇总（每15分钟刷新）
- `density_1hour`：1小时密度汇总（每小时刷新）

## 模型参数配置

所有物理模型参数通过JSON配置文件加载，文件位于：
`backend/config/model_params.json`

### 主要配置项

```json
{
  "physical_properties": {
    "gravity": 9.81,
    "kinematic_viscosity": 1.5e-7,
    "thermal_diffusivity": 1.2e-7,
    "thermal_expansion_coefficient": 0.001
  },
  "numerical_method": {
    "grid_points": 50,
    "initial_time_step": 0.1,
    "initial_under_relaxation": 0.5,
    "cfl_limit": 0.5
  },
  "stability_analysis": { ... },
  "risk_calculation": { ... },
  "alarm_thresholds": { ... },
  "tank_specs": { ... }
}
```

修改配置后重启Go服务生效：
```bash
docker-compose restart backend
```

## 告警配置

### 两级告警机制

| 级别 | 触发条件 | 动作 |
|------|----------|------|
| **一级（翻滚预警）** | 层间温差 > 8℃ **且** 密度差 > 2 kg/m³ | OPC UA推送告警 + 建议开启低压泵循环混合 |
| **二级（超压告警）** | 罐压 > 设计压力 × 90% | OPC UA推送告警 + 启动BOG压缩机自动调节 |

### 告警推送特性
- 断线自动重连（指数退避）
- 10秒心跳检测
- 告警缓存（最多100条），断线恢复后自动补发
- 告警去重（同一条件30秒内不重复推送）

## 模块通信协议

模块间通过Go Channel传递消息，消息类型定义在 `backend/messages/messages.go`

### 消息流

```
modbus_poller
    ↓ PollResult { TankID, DataType, Data, CollectedAt }
main.go (数据聚合层)
    ↓ PredictionRequest { TankID, Temperatures, Densities, Pressure }
rollover_predictor
    ↓ PredictionResult { TankID, RiskIndex, RiskLevel, CriticalTime, ... }
alarm_forwarder
    ↓ ForwardResult { TankID, Success, AlarmLevel, Message }
    ↓ ControlCommand { TankID, CommandType, Parameters }
modbus_poller (写入寄存器)
```

## 本地开发

### Go后端开发

```bash
cd backend

# 安装依赖
go mod download

# 本地运行（需要TimescaleDB和模拟器）
go run .

# 构建静态二进制
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -ldflags '-w -s -linkmode external -extldflags "-static"' \
    -o lng-monitor .
```

### 前端开发

```bash
cd frontend

# 本地开发服务器
python -m http.server 8080

# 构建生产镜像
docker build -f Dockerfile.frontend -t lng-frontend .
```

### 运行测试

```bash
# Go单元测试
cd backend && go test ./...

# 模拟器手动测试
# 1. 启动Modbus模拟器
python simulator/modbus_simulator.py --port 5020

# 2. 触发翻滚事件
curl "http://localhost:8000/api/induce_rollover?tank_id=1"

# 3. 检查告警是否产生
curl "http://localhost:8080/api/alarms/active"
```

## 目录结构

```
AI_solo_coder_task_A_039/
├── backend/
│   ├── modbus_poller/         # Modbus采集模块
│   ├── rollover_predictor/    # 预测模型模块
│   ├── alarm_forwarder/       # 告警转发模块
│   ├── messages/              # 消息定义
│   ├── config/                # 配置（含model_params.json）
│   ├── models/                # 数据模型
│   ├── database/              # 数据库操作
│   ├── api/                   # HTTP API
│   ├── main.go                # 主程序入口
│   ├── Dockerfile             # Go多阶段构建
│   └── go.mod
├── frontend/
│   ├── js/
│   │   ├── tank_3d_viewer.js  # 三维视图模块
│   │   └── risk_dashboard.js  # 仪表盘模块
│   ├── css/
│   ├── index.html
│   ├── nginx.conf             # Nginx配置（Gzip）
│   └── Dockerfile.frontend
├── simulator/
│   ├── modbus_simulator.py    # Modbus模拟器（增强版）
│   ├── opcua_simulator.py     # OPC UA模拟器
│   ├── Dockerfile.modbus
│   ├── Dockerfile.opcua
│   └── requirements.txt
├── database/
│   ├── init.sql               # 数据库初始化
│   └── timescale-config.sh    # 压缩+保留策略
├── monitoring/
│   └── prometheus.yml         # Prometheus配置
├── docker-compose.yml         # 服务编排
└── README.md
```

## 技术栈

### 后端
- **Go 1.21** - 高性能后端服务
- **Gin** - HTTP API框架
- **pgx** - PostgreSQL/TimescaleDB驱动
- **gopcua** - OPC UA客户端
- **pyModbusTCP** - Modbus协议
- **Prometheus Client** - 指标暴露
- **pprof** - 性能分析

### 数据库
- **TimescaleDB 2.13** - 时序数据库（PostgreSQL 16）
- 连续聚合、自动压缩、数据保留策略

### 前端
- **Three.js r149** - 3D可视化
- **ECharts 5** - 图表库
- **Nginx** - Web服务器（Gzip压缩）

### DevOps
- **Docker** - 容器化
- **Docker Compose** - 编排
- **Prometheus** - 监控

## 常见问题

### Q: Modbus连接失败？
A: 检查5020端口是否被占用，Linux下端口<1024需要root权限，可使用5020端口映射。

### Q: 前端页面无法加载数据？
A: 检查backend容器是否健康，查看API日志：`docker-compose logs backend`

### Q: TimescaleDB启动慢？
A: 首次启动需要初始化数据库和创建超表，约需1-2分钟。

### Q: 如何调整数据保留时间？
A: 修改 `database/timescale-config.sh` 中的保留策略，重新执行脚本或重建数据库。

### Q: 模拟器数据更新太快/太慢？
A: 通过API调整：`curl "http://localhost:8000/api/set_interval?seconds=15"`

## License

MIT License
