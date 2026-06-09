package config

import (
	"os"
	"strconv"
)

type Config struct {
	Database   DatabaseConfig
	Modbus     ModbusConfig
	OPCUA      OPCUAConfig
	Server     ServerConfig
	Alarm      AlarmConfig
	Prediction PredictionConfig
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

type ModbusConfig struct {
	Host        string
	Port        int
	SlaveID     byte
	IntervalSec int
	TankCount   int
	Layers      int
	ThermoPerLayer int
	DensityMeters int
}

type OPCUAConfig struct {
	Endpoint string
	NodeID   string
}

type ServerConfig struct {
	Host string
	Port int
}

type AlarmConfig struct {
	TempDiffThreshold    float64
	DensityDiffThreshold float64
	PressureThresholdPct float64
}

type PredictionConfig struct {
	IntervalSec      int
	ModelVersion     string
	GridPoints       int
	TimeSteps        int
	StabilityThreshold float64
}

func Load() *Config {
	return &Config{
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvInt("DB_PORT", 5432),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			DBName:   getEnv("DB_NAME", "lng_monitoring"),
		},
		Modbus: ModbusConfig{
			Host:           getEnv("MODBUS_HOST", "localhost"),
			Port:           getEnvInt("MODBUS_PORT", 502),
			SlaveID:        byte(getEnvInt("MODBUS_SLAVE", 1)),
			IntervalSec:    getEnvInt("MODBUS_INTERVAL", 30),
			TankCount:      getEnvInt("TANK_COUNT", 4),
			Layers:         getEnvInt("LAYERS", 5),
			ThermoPerLayer: getEnvInt("THERMO_PER_LAYER", 8),
			DensityMeters:  getEnvInt("DENSITY_METERS", 3),
		},
		OPCUA: OPCUAConfig{
			Endpoint: getEnv("OPCUA_ENDPOINT", "opc.tcp://localhost:4840"),
			NodeID:   getEnv("OPCUA_NODE", "ns=2;s=Alarm"),
		},
		Server: ServerConfig{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnvInt("SERVER_PORT", 8080),
		},
		Alarm: AlarmConfig{
			TempDiffThreshold:    getEnvFloat("TEMP_DIFF_THRESHOLD", 8.0),
			DensityDiffThreshold: getEnvFloat("DENSITY_DIFF_THRESHOLD", 2.0),
			PressureThresholdPct: getEnvFloat("PRESSURE_THRESHOLD_PCT", 90.0),
		},
		Prediction: PredictionConfig{
			IntervalSec:          getEnvInt("PREDICTION_INTERVAL", 300),
			ModelVersion:         "1.0",
			GridPoints:           getEnvInt("GRID_POINTS", 50),
			TimeSteps:            getEnvInt("TIME_STEPS", 1000),
			StabilityThreshold:   getEnvFloat("STABILITY_THRESHOLD", 0.5),
		},
	}
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value, exists := os.LookupEnv(key); exists {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}
