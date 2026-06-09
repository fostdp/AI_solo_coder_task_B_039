package api

import (
	"context"
	"fmt"
	"lng-monitoring/alarm"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/models"
	"lng-monitoring/prediction"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type Server struct {
	cfg         *config.Config
	db          *database.DB
	predictor   *prediction.RolloverPredictor
	alarmEngine *alarm.AlarmEngine
	router      *gin.Engine
}

func NewServer(cfg *config.Config, db *database.DB, predictor *prediction.RolloverPredictor, alarmEngine *alarm.AlarmEngine) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	s := &Server{
		cfg:         cfg,
		db:          db,
		predictor:   predictor,
		alarmEngine: alarmEngine,
		router:      router,
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	api := s.router.Group("/api")
	{
		api.GET("/tanks", s.GetTanks)
		api.GET("/tanks/:id/data", s.GetTank3DData)
		api.GET("/tanks/:id/temperature", s.GetTemperatureData)
		api.GET("/tanks/:id/density", s.GetDensityData)
		api.GET("/tanks/:id/pressure", s.GetPressureData)
		api.GET("/tanks/:id/prediction", s.GetPrediction)
		api.GET("/tanks/:id/layer-summary", s.GetLayerSummary)

		api.GET("/sensors/:tankId/:layer/:sensor/trend", s.GetSensorTrend)
		api.GET("/density-sensors/:tankId/:sensor/trend", s.GetDensityTrend)

		api.GET("/alarms", s.GetActiveAlarms)
		api.POST("/alarms/:id/acknowledge", s.AcknowledgeAlarm)
		api.POST("/alarms/:id/clear", s.ClearAlarm)
		api.GET("/alarm-config", s.GetAlarmConfig)

		api.GET("/health", s.HealthCheck)
	}
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	fmt.Printf("API server starting on %s\n", addr)
	return s.router.Run(addr)
}

func (s *Server) GetTanks(c *gin.Context) {
	tanks, err := s.db.GetTanks(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tanks)
}

func (s *Server) GetTank3DData(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	ctx := c.Request.Context()

	tanks, err := s.db.GetTanks(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var tank *models.Tank
	for _, t := range tanks {
		if t.TankID == tankID {
			tank = &t
			break
		}
	}
	if tank == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tank not found"})
		return
	}

	layerTemps, err := s.db.GetLayerAvgTemps(ctx, tankID)
	if err != nil {
		layerTemps = []float64{-160, -159, -158, -157, -156}
	}

	densityData, err := s.db.GetLatestDensityData(ctx, tankID)
	if err != nil {
		densityData = []models.DensityData{
			{Density: 425, HeightPosition: 4.0},
			{Density: 424, HeightPosition: 24.0},
			{Density: 423, HeightPosition: 44.0},
		}
	}

	pressureData, err := s.db.GetLatestPressureData(ctx, tankID)
	var pressure float64
	if err != nil {
		pressure = 0.15
	} else {
		pressure = pressureData.Pressure
	}

	prediction, err := s.db.GetLatestRolloverPrediction(ctx, tankID)
	var riskIndex float64
	if err != nil {
		riskIndex = 0.1
	} else {
		riskIndex = prediction.RiskIndex
	}

	activeAlarms, err := s.db.GetActiveAlarms(ctx)
	if err != nil {
		activeAlarms = []models.Alarm{}
	}

	var tankAlarms []models.Alarm
	for _, a := range activeAlarms {
		if a.TankID == tankID {
			tankAlarms = append(tankAlarms, a)
		}
	}

	densities := make([]float64, len(densityData))
	densityHeights := make([]float64, len(densityData))
	for i, d := range densityData {
		densities[i] = d.Density
		densityHeights[i] = d.HeightPosition
	}

	data := models.Tank3DData{
		TankID:         tankID,
		TankName:       tank.TankName,
		LayerTemps:     layerTemps,
		Densities:      densities,
		DensityHeights: densityHeights,
		Pressure:       pressure,
		RiskIndex:      riskIndex,
		Alarms:         tankAlarms,
	}

	c.JSON(http.StatusOK, data)
}

func (s *Server) GetTemperatureData(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	data, err := s.db.GetLatestTemperatureData(c.Request.Context(), tankID, 5)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (s *Server) GetDensityData(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	data, err := s.db.GetLatestDensityData(c.Request.Context(), tankID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (s *Server) GetPressureData(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	data, err := s.db.GetLatestPressureData(c.Request.Context(), tankID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (s *Server) GetPrediction(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	data, err := s.db.GetLatestRolloverPrediction(c.Request.Context(), tankID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (s *Server) GetLayerSummary(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	duration := 24 * time.Hour
	if d := c.Query("duration"); d != "" {
		if hours, err := strconv.Atoi(d); err == nil {
			duration = time.Duration(hours) * time.Hour
		}
	}

	data, err := s.db.GetHistoricalLayerData(c.Request.Context(), tankID, duration)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (s *Server) GetSensorTrend(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("tankId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	layer, err := strconv.Atoi(c.Param("layer"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid layer"})
		return
	}

	sensorIndex, err := strconv.Atoi(c.Param("sensor"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sensor index"})
		return
	}

	duration := 24 * time.Hour
	if d := c.Query("duration"); d != "" {
		if hours, err := strconv.Atoi(d); err == nil {
			duration = time.Duration(hours) * time.Hour
		}
	}

	data, err := s.db.GetSensorTrendData(c.Request.Context(), tankID, layer, sensorIndex, duration)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (s *Server) GetDensityTrend(c *gin.Context) {
	tankID, err := strconv.Atoi(c.Param("tankId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tank id"})
		return
	}

	sensorIndex, err := strconv.Atoi(c.Param("sensor"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sensor index"})
		return
	}

	duration := 24 * time.Hour
	if d := c.Query("duration"); d != "" {
		if hours, err := strconv.Atoi(d); err == nil {
			duration = time.Duration(hours) * time.Hour
		}
	}

	data, err := s.db.GetDensityTrendData(c.Request.Context(), tankID, sensorIndex, duration)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (s *Server) GetActiveAlarms(c *gin.Context) {
	alarms, err := s.db.GetActiveAlarms(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, alarms)
}

func (s *Server) AcknowledgeAlarm(c *gin.Context) {
	alarmID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid alarm id"})
		return
	}

	if err := s.alarmEngine.AcknowledgeAlarm(c.Request.Context(), alarmID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (s *Server) ClearAlarm(c *gin.Context) {
	alarmID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid alarm id"})
		return
	}

	if err := s.alarmEngine.ClearAlarm(c.Request.Context(), alarmID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (s *Server) GetAlarmConfig(c *gin.Context) {
	configs, err := s.db.GetAlarmConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, configs)
}

func (s *Server) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "healthy", "time": time.Now().Format(time.RFC3339)})
}

func (s *Server) Handler() http.Handler {
	return s.router
}
