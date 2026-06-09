# LNG储罐翻滚预测与安全监控系统

## 系统概述

本系统是一套完整的大型液化天然气(LNG)储罐翻滚预测与安全监控全栈应用。系统针对4座16万立方米LNG储罐，实现实时数据采集、翻滚风险预测、多级告警和三维可视化监控。

## 系统架构

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Modbus TCP     │────▶│   Go 后端服务   │────▶│  TimescaleDB    │
│  模拟器/设备    │     │  (数据采集/处   │     │  (时序数据库)  │
│                 │     │   理/预测/告警) │     │                 │
└─────────────────┘     └─────────────────┘     └─────────────────┘
                                  │
                                  ▼
                        ┌─────────────────┐
                        │   前端可视化    │
                        │ (Three.js 3D)   │
                        └─────────────────┘
                                  │
                                  ▼
                        ┌─────────────────┐
                        │   OPC UA/DCS    │
                        │   告警推送      │
                        └─────────────────┘
```

## 功能特性

### 数据采集
- 每30秒通过Modbus TCP采集传感器数据
- 每座储罐5层温度计阵列（每层8个）
- 每座储罐3台密度计
- 罐顶压力变送器数据
- BOG压缩机状态监测

### 翻滚预测模型
- 基于有限体积法求解分层对流方程
- 温度梯度和密度梯度分析
- 分层稳定性评估
- 临界失稳时间预测
- 风险指数计算（0-100%）

### 告警系统
- **一级翻滚预警**：层间温差>8℃ 且 密度差>2kg/m³
- **二级超压告警**：罐压超过设计压力90%
- 告警通过OPC UA推送至DCS
- BOG压缩机自动调节控制
- 建议开启低压泵循环混合

### 前端可视化
- Three.js 3D储罐模型
- Canvas剖面图显示
- 温度分层颜色渲染
- 密度等值线标注
- 传感器点击交互
- 24小时趋势曲线（ECharts）
- 风险仪表盘

## 项目结构

```
AI_solo_coder_task_A_039/
├── backend/                    # Go后端服务
│   ├── main.go                 # 主程序入口
│   ├── go.mod                  # 依赖管理
│   ├── .env.example            # 环境变量示例
│   ├── config/                 # 配置模块
│   │   └── config.go
│   ├── models/                 # 数据模型
│   │   └── models.go
│   ├── database/               # 数据库操作
│   │   └── database.go
│   ├── modbus/                 # Modbus TCP采集
│   │   └── client.go
│   ├── prediction/             # 翻滚预测模型
│   │   └── rollover_model.go
│   ├── alarm/                  # 告警引擎
│   │   ├── engine.go
│   │   └── opcua.go
│   └── api/                    # RESTful API
│       └── server.go
├── frontend/                   # 前端应用
│   ├── index.html              # 主页面
│   ├── css/
│   │   └── style.css           # 样式文件
│   └── js/
│       ├── config.js           # 前端配置
│       ├── api.js              # API调用
│       ├── visualization.js    # 可视化工具
│       ├── threeScene.js       # Three.js 3D场景
│       └── main.js             # 主程序
├── database/                   # 数据库脚本
│   └── init.sql                # TimescaleDB初始化
└── simulator/                  # Modbus TCP模拟器
    ├── modbus_simulator.py     # 模拟器主程序
    └── requirements.txt        # Python依赖
```

## 快速开始

### 1. 数据库初始化

```bash
# 安装TimescaleDB
# 参考: https://docs.timescale.com/install/latest/

# 执行初始化脚本
psql -U postgres -f database/init.sql
```

### 2. 启动Modbus TCP模拟器

```bash
cd simulator
pip install -r requirements.txt
python modbus_simulator.py --port 5020

# 可用命令:
#   status          - 查看当前状态
#   rollover <1-4>  - 手动触发指定储罐翻滚事件
#   help            - 显示帮助
```

### 3. 启动Go后端服务

```bash
cd backend

# 复制并配置环境变量
cp .env.example .env
# 编辑 .env 文件，根据实际情况修改配置

# 安装依赖
go mod tidy

# 编译并运行
go build -o lng-monitor
./lng-monitor
```

### 4. 启动前端应用

```bash
cd frontend

# 使用任意静态文件服务器，例如:
python -m http.server 8081

# 或使用Node.js:
npx serve .
```

### 5. 访问系统

打开浏览器访问: `http://localhost:8081`

## API接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/tanks` | 获取储罐列表 |
| GET | `/api/tanks/:id/data` | 获取储罐3D可视化数据 |
| GET | `/api/tanks/:id/temperature` | 获取温度数据 |
| GET | `/api/tanks/:id/density` | 获取密度数据 |
| GET | `/api/tanks/:id/pressure` | 获取压力数据 |
| GET | `/api/tanks/:id/prediction` | 获取翻滚预测结果 |
| GET | `/api/sensors/:tankId/:layer/:sensor/trend` | 获取传感器温度趋势 |
| GET | `/api/density-sensors/:tankId/:sensor/trend` | 获取密度趋势 |
| GET | `/api/alarms` | 获取活动告警 |
| POST | `/api/alarms/:id/acknowledge` | 确认告警 |
| POST | `/api/alarms/:id/clear` | 清除告警 |
| GET | `/api/health` | 健康检查 |

## Modbus寄存器映射

每座储罐占用1000个寄存器地址，基地址为 `(tank_id - 1) * 1000`

| 地址范围 | 数据类型 | 说明 |
|----------|----------|------|
| 0-39 | float32 | 5层×8个温度传感器 |
| 500-505 | float32 | 3个密度计 |
| 600-601 | float32 | 罐顶压力 |
| 700 | uint16 | 1#压缩机状态 |
| 701-708 | float32 | 1#压缩机振动/电流/排气压力 |
| 710 | uint16 | 2#压缩机状态 |
| 711-718 | float32 | 2#压缩机参数 |

## 翻滚预测模型算法

### 有限体积法求解分层对流方程

1. **控制方程**：
   - 连续性方程: ∂ρ/∂t + ∂(ρu)/∂z = 0
   - 动量方程: ∂u/∂t + u∂u/∂z = -g/ρ ∂ρ/∂z + ν ∂²u/∂z²

2. **分层稳定性评估**：
   - 浮力频率 N² = -g/ρ ∂ρ/∂z
   - 稳定性指数 = 1 - exp(-N² × 100)

3. **风险指数计算**：
   - 温度差权重: 35%
   - 密度差权重: 25%
   - 不稳定性权重: 25%
   - 临界时间权重: 15%

## 技术栈

### 后端
- **语言**: Go 1.21
- **Web框架**: Gin
- **数据库**: PostgreSQL + TimescaleDB
- **Modbus**: goburrow/modbus
- **OPC UA**: gopcua/opcua
- **数值计算**: gonum

### 前端
- **3D渲染**: Three.js r160
- **图表**: ECharts 5.4
- **HTTP客户端**: Fetch API
- **样式**: CSS3

### 数据库
- **时序数据库**: TimescaleDB
- **特性**: 超表、连续聚合、自动分区

## 告警级别说明

| 级别 | 类型 | 触发条件 | 建议动作 |
|------|------|----------|----------|
| 1 | 翻滚预警 | 层间温差>8℃ 且 密度差>2kg/m³ | 开启低压泵循环混合 |
| 2 | 超压告警 | 罐压>设计压力90% | 检查BOG压缩机，紧急泄压 |

## 生产部署建议

1. **高可用性**：
   - 使用Kubernetes部署Go后端
   - TimescaleDB配置流复制
   - Modbus连接冗余配置

2. **数据保留**：
   - 原始数据保留3个月
   - 15分钟聚合数据保留1年
   - 1小时聚合数据永久保留

3. **安全**：
   - 启用TLS加密
   - API使用JWT认证
   - Modbus TCP使用VPN隔离
   - OPC UA使用安全策略

## 许可证

本项目仅供学习和研究使用。

## 联系方式

如有技术问题，请查看各模块源代码中的详细注释。
