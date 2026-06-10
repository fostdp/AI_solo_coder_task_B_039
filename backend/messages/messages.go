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

type BOGBatch struct {
	TankID      int
	Data        []models.BOGCompressorData
	CollectedAt time.Time
}

type BOGDiagnosticRequest struct {
	TankID         int
	CompressorID   int
	BOGHistory     []models.BOGCompressorData
	CollectedAt    time.Time
}

type BOGDiagnosticResult struct {
	TankID          int
	CompressorID    int
	AnomalyScore    float64
	IsAnomaly       bool
	AnomalyType     string
	Confidence      float64
	RemainingHours  float64
	Recommendation  string
	DiagnosedAt     time.Time
	ErrorMessage    string
}

type HeatLeakRequest struct {
	TankID              int
	TemperatureHistory  []models.LayerSummary
	AmbientTemperature  float64
	CollectedAt         time.Time
}

type HeatLeakResult struct {
	TankID                 int
	EquivalentConductivity float64
	InsulationPerformance  float64
	HeatLeakRate           float64
	LeakRegion             []int
	IsWarning              bool
	TotalHeatLoadKW        float64
	LastCalibratedAt       time.Time
	EvaluatedAt            time.Time
	ErrorMessage           string
}

type FlowRateChange struct {
	ChangeTimeHours float64
	NewFlowRate     float64
}

type UnloadingRequest struct {
	TankID              int
	UnloadingRate       float64
	UnloadingDensity    float64
	UnloadingTemp       float64
	InitialTemps        []float64
	InitialDensities    []float64
	EstimatedDuration   float64
	RequestedAt         time.Time
	FlowRateChanges     []FlowRateChange
}

type UnloadingPrediction struct {
	TankID              int
	PredictedTemps      [][]float64
	PredictedDensities  [][]float64
	TimeSteps           []float64
	MaxTempDiff         float64
	MaxDensityDiff      float64
	OptimalPumpOnTime   float64
	RolloverRisk        float64
	PredictedAt         time.Time
	ErrorMessage        string
	AsyncComputed       bool
}

type SchedulerRequest struct {
	TankStates      []TankStateForScheduler
	CollectedAt     time.Time
}

type TankStateForScheduler struct {
	TankID      int
	Level       float64
	AvgTemp     float64
	RiskIndex   float64
	Pressure    float64
	HasBOGComp1 bool
	HasBOGComp2 bool
}

type ScheduleResult struct {
	CompressorLoads   map[string]float64
	PumpOperations    []PumpOperation
	EvaporationLoss   float64
	OptimizedAt       time.Time
	ErrorMessage      string
	Decomposed        bool
	SubproblemCount   int
	AsyncOptimized    bool
}

type PumpOperation struct {
	TankID    int
	PumpID    int
	StartTime float64
	Duration  float64
	Action    string
}
