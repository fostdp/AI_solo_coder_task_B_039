-- LNG储罐翻滚预测与安全监控系统 - TimescaleDB初始化脚本
-- 创建数据库
CREATE DATABASE lng_monitoring;
\c lng_monitoring;

-- 启用TimescaleDB扩展
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- 储罐信息表
CREATE TABLE IF NOT EXISTS tanks (
    tank_id SERIAL PRIMARY KEY,
    tank_name VARCHAR(50) NOT NULL,
    capacity NUMERIC(12,2) NOT NULL,
    design_pressure NUMERIC(8,4) NOT NULL,
    height NUMERIC(8,2) NOT NULL,
    diameter NUMERIC(8,2) NOT NULL,
    layers INTEGER NOT NULL DEFAULT 5,
    thermometers_per_layer INTEGER NOT NULL DEFAULT 8,
    density_meters INTEGER NOT NULL DEFAULT 3,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 初始化4座16万立方米储罐
INSERT INTO tanks (tank_name, capacity, design_pressure, height, diameter) VALUES
('T-101', 160000.00, 0.25, 48.0, 82.0),
('T-102', 160000.00, 0.25, 48.0, 82.0),
('T-103', 160000.00, 0.25, 48.0, 82.0),
('T-104', 160000.00, 0.25, 48.0, 82.0);

-- 温度数据超表
CREATE TABLE IF NOT EXISTS temperature_data (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    layer INTEGER NOT NULL,
    sensor_index INTEGER NOT NULL,
    temperature NUMERIC(8,4) NOT NULL,
    modbus_address INTEGER NOT NULL,
    CONSTRAINT pk_temperature_data PRIMARY KEY (time, tank_id, layer, sensor_index)
);

-- 创建超表（按时间分区，1天一个分区）
SELECT create_hypertable('temperature_data', 'time', 
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_temperature_tank_time ON temperature_data (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_temperature_tank_layer ON temperature_data (tank_id, layer, time DESC);

-- 密度数据超表
CREATE TABLE IF NOT EXISTS density_data (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    sensor_index INTEGER NOT NULL,
    density NUMERIC(10,4) NOT NULL,
    height_position NUMERIC(6,2) NOT NULL,
    modbus_address INTEGER NOT NULL,
    CONSTRAINT pk_density_data PRIMARY KEY (time, tank_id, sensor_index)
);

SELECT create_hypertable('density_data', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_density_tank_time ON density_data (tank_id, time DESC);

-- 压力数据超表
CREATE TABLE IF NOT EXISTS pressure_data (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    pressure NUMERIC(10,6) NOT NULL,
    modbus_address INTEGER NOT NULL,
    CONSTRAINT pk_pressure_data PRIMARY KEY (time, tank_id)
);

SELECT create_hypertable('pressure_data', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_pressure_tank_time ON pressure_data (tank_id, time DESC);

-- BOG压缩机状态数据超表
CREATE TABLE IF NOT EXISTS bog_compressor_data (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    compressor_id INTEGER NOT NULL,
    running_status INTEGER NOT NULL,
    vibration_level NUMERIC(8,4),
    motor_current NUMERIC(8,4),
    discharge_pressure NUMERIC(10,6),
    modbus_address INTEGER NOT NULL,
    CONSTRAINT pk_bog_data PRIMARY KEY (time, tank_id, compressor_id)
);

SELECT create_hypertable('bog_compressor_data', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_bog_tank_time ON bog_compressor_data (tank_id, time DESC);

-- 翻滚风险预测结果表
CREATE TABLE IF NOT EXISTS rollover_prediction (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    risk_index NUMERIC(8,4) NOT NULL,
    max_temp_diff NUMERIC(8,4) NOT NULL,
    max_density_diff NUMERIC(10,4) NOT NULL,
    critical_time_hours NUMERIC(10,4),
    stratification_stability NUMERIC(8,4),
    convection_velocity NUMERIC(10,6),
    recommendation VARCHAR(200),
    model_version VARCHAR(20) NOT NULL DEFAULT '1.0',
    CONSTRAINT pk_rollover_prediction PRIMARY KEY (time, tank_id)
);

SELECT create_hypertable('rollover_prediction', 'time',
    chunk_time_interval => INTERVAL '1 hour',
    if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_rollover_tank_time ON rollover_prediction (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_rollover_risk ON rollover_prediction (risk_index DESC, time DESC);

-- 告警记录表
CREATE TABLE IF NOT EXISTS alarms (
    alarm_id SERIAL PRIMARY KEY,
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    alarm_level INTEGER NOT NULL,
    alarm_type VARCHAR(50) NOT NULL,
    alarm_message VARCHAR(500) NOT NULL,
    threshold_value NUMERIC(12,4),
    actual_value NUMERIC(12,4),
    acknowledged BOOLEAN NOT NULL DEFAULT FALSE,
    acknowledged_time TIMESTAMPTZ,
    cleared BOOLEAN NOT NULL DEFAULT FALSE,
    cleared_time TIMESTAMPTZ,
    opcua_pushed BOOLEAN NOT NULL DEFAULT FALSE,
    opcua_push_time TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alarms_tank_time ON alarms (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_level ON alarms (alarm_level, time DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_active ON alarms (cleared, time DESC);

-- 分层数据汇总表（用于快速查询）
CREATE TABLE IF NOT EXISTS layer_summary (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    layer INTEGER NOT NULL,
    avg_temp NUMERIC(8,4) NOT NULL,
    min_temp NUMERIC(8,4) NOT NULL,
    max_temp NUMERIC(8,4) NOT NULL,
    temp_stddev NUMERIC(8,4),
    CONSTRAINT pk_layer_summary PRIMARY KEY (time, tank_id, layer)
);

SELECT create_hypertable('layer_summary', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

-- 连续聚合视图：15分钟温度汇总
CREATE MATERIALIZED VIEW IF NOT EXISTS temperature_15min
WITH (timescaledb.continuous) AS
SELECT
    tank_id,
    layer,
    time_bucket('15 minutes', time) AS bucket,
    AVG(temperature) AS avg_temp,
    MIN(temperature) AS min_temp,
    MAX(temperature) AS max_temp
FROM temperature_data
GROUP BY tank_id, layer, time_bucket('15 minutes', time)
WITH NO DATA;

-- 连续聚合视图：1小时密度汇总
CREATE MATERIALIZED VIEW IF NOT EXISTS density_1hour
WITH (timescaledb.continuous) AS
SELECT
    tank_id,
    sensor_index,
    time_bucket('1 hour', time) AS bucket,
    AVG(density) AS avg_density,
    MIN(density) AS min_density,
    MAX(density) AS max_density
FROM density_data
GROUP BY tank_id, sensor_index, time_bucket('1 hour', time)
WITH NO DATA;

-- 告警配置表
CREATE TABLE IF NOT EXISTS alarm_config (
    config_id SERIAL PRIMARY KEY,
    alarm_type VARCHAR(50) NOT NULL UNIQUE,
    alarm_level INTEGER NOT NULL,
    temp_threshold NUMERIC(8,4),
    density_threshold NUMERIC(10,4),
    pressure_threshold_pct NUMERIC(6,2),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    description VARCHAR(200),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO alarm_config (alarm_type, alarm_level, temp_threshold, density_threshold, description) VALUES
('ROLLOVER_WARNING', 1, 8.0, 2.0, '一级翻滚预警：层间温差>8℃且密度差>2kg/m³');

INSERT INTO alarm_config (alarm_type, alarm_level, pressure_threshold_pct, description) VALUES
('OVERPRESSURE_ALARM', 2, 90.0, '二级超压告警：罐压超过设计压力90%');

-- 用户权限表
CREATE TABLE IF NOT EXISTS users (
    user_id SERIAL PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'operator',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO users (username, password_hash, role) VALUES
('admin', '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', 'admin'),
('operator', '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', 'operator');

-- BOG压缩机故障诊断结果表
CREATE TABLE IF NOT EXISTS bog_diagnostic (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    compressor_id INTEGER NOT NULL,
    anomaly_score NUMERIC(8,4) NOT NULL,
    is_anomaly BOOLEAN NOT NULL DEFAULT FALSE,
    anomaly_type VARCHAR(50),
    confidence NUMERIC(8,4),
    remaining_hours NUMERIC(10,4),
    recommendation VARCHAR(500),
    vibration_trend NUMERIC(8,4),
    current_trend NUMERIC(8,4),
    model_version VARCHAR(20) NOT NULL DEFAULT '1.0',
    CONSTRAINT pk_bog_diagnostic PRIMARY KEY (time, tank_id, compressor_id)
);

SELECT create_hypertable('bog_diagnostic', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_bog_diagnostic_tank_time ON bog_diagnostic (tank_id, compressor_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_bog_diagnostic_anomaly ON bog_diagnostic (is_anomaly, time DESC);

-- 储罐漏热评估结果表
CREATE TABLE IF NOT EXISTS heat_leak_assessment (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    equivalent_conductivity NUMERIC(10,6) NOT NULL,
    insulation_performance NUMERIC(8,4) NOT NULL,
    heat_leak_rate NUMERIC(10,4) NOT NULL,
    leak_regions INTEGER[],
    is_warning BOOLEAN NOT NULL DEFAULT FALSE,
    total_heat_load_kw NUMERIC(10,4) NOT NULL,
    ambient_temp NUMERIC(8,4),
    inner_temp NUMERIC(8,4),
    model_version VARCHAR(20) NOT NULL DEFAULT '1.0',
    CONSTRAINT pk_heat_leak_assessment PRIMARY KEY (time, tank_id)
);

SELECT create_hypertable('heat_leak_assessment', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_heat_leak_tank_time ON heat_leak_assessment (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_heat_leak_warning ON heat_leak_assessment (is_warning, time DESC);

-- 卸船预测结果表
CREATE TABLE IF NOT EXISTS unloading_prediction (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL REFERENCES tanks(tank_id),
    unloading_rate NUMERIC(12,4) NOT NULL,
    unloading_density NUMERIC(10,4) NOT NULL,
    unloading_temp NUMERIC(8,4) NOT NULL,
    estimated_duration NUMERIC(8,4) NOT NULL,
    max_temp_diff NUMERIC(8,4) NOT NULL,
    max_density_diff NUMERIC(10,4) NOT NULL,
    optimal_pump_on_time NUMERIC(8,4),
    rollover_risk NUMERIC(8,4) NOT NULL,
    time_steps NUMERIC(8,4)[],
    predicted_temps NUMERIC(8,4)[][],
    predicted_densities NUMERIC(10,4)[][],
    model_version VARCHAR(20) NOT NULL DEFAULT '1.0',
    CONSTRAINT pk_unloading_prediction PRIMARY KEY (time, tank_id)
);

SELECT create_hypertable('unloading_prediction', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_unloading_tank_time ON unloading_prediction (tank_id, time DESC);

-- 多罐联合调度结果表
CREATE TABLE IF NOT EXISTS multi_tank_schedule (
    time TIMESTAMPTZ NOT NULL,
    compressor_loads JSONB NOT NULL,
    pump_operations JSONB,
    evaporation_loss_kg NUMERIC(12,4) NOT NULL,
    evaporation_loss_m3 NUMERIC(12,4) NOT NULL,
    objective_value NUMERIC(12,4),
    optimization_status VARCHAR(50) NOT NULL DEFAULT 'OPTIMAL',
    model_version VARCHAR(20) NOT NULL DEFAULT '1.0',
    CONSTRAINT pk_multi_tank_schedule PRIMARY KEY (time)
);

SELECT create_hypertable('multi_tank_schedule', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

-- 环境温度数据表（用于漏热评估）
CREATE TABLE IF NOT EXISTS ambient_temperature_data (
    time TIMESTAMPTZ NOT NULL,
    location_id INTEGER NOT NULL DEFAULT 1,
    temperature NUMERIC(8,4) NOT NULL,
    humidity NUMERIC(8,4),
    wind_speed NUMERIC(8,4),
    solar_radiation NUMERIC(10,4),
    CONSTRAINT pk_ambient_temp PRIMARY KEY (time, location_id)
);

SELECT create_hypertable('ambient_temperature_data', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE);

-- 新增告警类型
INSERT INTO alarm_config (alarm_type, alarm_level, temp_threshold, description) VALUES
('BOG_COMPRESSOR_FAULT', 1, NULL, 'BOG压缩机故障预警：振动或电流异常')
ON CONFLICT (alarm_type) DO NOTHING;

INSERT INTO alarm_config (alarm_type, alarm_level, pressure_threshold_pct, description) VALUES
('HEAT_LEAK_WARNING', 1, NULL, '储罐保冷性能下降预警：导热系数升高超过阈值')
ON CONFLICT (alarm_type) DO NOTHING;

INSERT INTO alarm_config (alarm_type, alarm_level, description) VALUES
('UNLOADING_RISK_WARNING', 1, '卸船过程翻滚风险预警：预测分层超限')
ON CONFLICT (alarm_type) DO NOTHING;

GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO postgres;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO postgres;
