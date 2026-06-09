package messages

import (
	"lng-monitoring/models"
	"time"
)

type DataType string

const (
	DataTypeTemperature DataType = "temperature"
	DataTypeDensity     DataType = "density"
	DataTypePressure    DataType = "pressure"
	DataTypeBOG         DataType = "bog"
)

type PollResult struct {
	TaskID      string
	TankID      int
	DataType    DataType
	Data        interface{}
	CollectedAt time.Time
	Error       error
}

type TemperatureBatch struct {
	TankID      int
	Data        []models.TemperatureData
	CollectedAt time.Time
}

type DensityBatch struct {
	TankID      int
	Data        []models.DensityData
	CollectedAt time.Time
}

type PressureData struct {
	TankID      int
	Data        models.PressureData
	CollectedAt time.Time
}

type PredictionRequest struct {
	TankID          int
	Temperatures    []models.TemperatureData
	Densities       []models.DensityData
	Pressure        float64
	CollectedAt     time.Time
}

type PredictionResult struct {
	TankID           int
	RiskIndex        float64
	RiskLevel        string
	CriticalTime     float64
	MaxTempDiff      float64
	MaxDensityDiff   float64
	BuoyancyFreq     float64
	InterfaceHeight  float64
	PredictedAt      time.Time
	ErrorMessage     string
}

type AlarmCommand struct {
	TankID         int
	AlarmType      string
	Level          int
	Message        string
	RiskIndex      float64
	Timestamp      time.Time
	ActionRequired string
	TargetPressure float64
}

type ControlCommand struct {
	CommandType   string
	TankID        int
	TargetValue   float64
	Timestamp     time.Time
}

type ForwardResult struct {
	Success   bool
	Command   ControlCommand
	Error     string
	Timestamp time.Time
}
