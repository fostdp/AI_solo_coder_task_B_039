#!/bin/bash
set -e

echo "=== Configuring TimescaleDB compression and retention policies ==="

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL

-- 启用压缩
ALTER TABLE temperature_data SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'tank_id, layer',
    timescaledb.compress_orderby = 'time DESC'
);

ALTER TABLE density_data SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'tank_id',
    timescaledb.compress_orderby = 'time DESC'
);

ALTER TABLE pressure_data SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'tank_id',
    timescaledb.compress_orderby = 'time DESC'
);

ALTER TABLE bog_compressor_data SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'tank_id, compressor_id',
    timescaledb.compress_orderby = 'time DESC'
);

ALTER TABLE layer_summary SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'tank_id, layer',
    timescaledb.compress_orderby = 'time DESC'
);

ALTER TABLE rollover_prediction SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'tank_id',
    timescaledb.compress_orderby = 'time DESC'
);

-- 创建压缩策略：7天后压缩
SELECT add_compression_policy('temperature_data', INTERVAL '7 days');
SELECT add_compression_policy('density_data', INTERVAL '7 days');
SELECT add_compression_policy('pressure_data', INTERVAL '7 days');
SELECT add_compression_policy('bog_compressor_data', INTERVAL '7 days');
SELECT add_compression_policy('layer_summary', INTERVAL '7 days');
SELECT add_compression_policy('rollover_prediction', INTERVAL '3 days');

-- 创建数据保留策略：原始数据保留3个月
SELECT add_retention_policy('temperature_data', INTERVAL '3 months');
SELECT add_retention_policy('density_data', INTERVAL '3 months');
SELECT add_retention_policy('pressure_data', INTERVAL '3 months');
SELECT add_retention_policy('bog_compressor_data', INTERVAL '3 months');
SELECT add_retention_policy('layer_summary', INTERVAL '6 months');
SELECT add_retention_policy('rollover_prediction', INTERVAL '1 year');

-- 刷新连续聚合策略
SELECT add_continuous_aggregate_policy('temperature_15min',
    start_offset => INTERVAL '1 hour',
    end_offset => INTERVAL '15 minutes',
    schedule_interval => INTERVAL '15 minutes'
);

SELECT add_continuous_aggregate_policy('density_1hour',
    start_offset => INTERVAL '4 hours',
    end_offset => INTERVAL '1 hour',
    schedule_interval => INTERVAL '1 hour'
);

-- 告警表保留1年
DELETE FROM alarms WHERE time < NOW() - INTERVAL '1 year';
CREATE INDEX IF NOT EXISTS idx_alarms_time ON alarms (time);

-- 重置连续聚合
CALL refresh_continuous_aggregate('temperature_15min', NULL, NULL);
CALL refresh_continuous_aggregate('density_1hour', NULL, NULL);

EOSQL

echo "=== TimescaleDB configuration completed ==="
