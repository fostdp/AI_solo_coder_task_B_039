package modbus

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/models"
	"math"
	"time"

	"github.com/goburrow/modbus"
)

type Collector struct {
	cfg    *config.ModbusConfig
	db     *database.DB
	client modbus.Client
	handler *modbus.TCPClientHandler
}

func NewCollector(cfg *config.ModbusConfig, db *database.DB) (*Collector, error) {
	handler := modbus.NewTCPClientHandler(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	handler.SlaveId = cfg.SlaveID
	handler.Timeout = 5 * time.Second

	client := modbus.NewClient(handler)

	return &Collector{
		cfg:     cfg,
		db:      db,
		client:  client,
		handler: handler,
	}, nil
}

func (c *Collector) Close() error {
	if c.handler != nil {
		return c.handler.Close()
	}
	return nil
}

func (c *Collector) Connect() error {
	return c.handler.Connect()
}

func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(c.cfg.IntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.CollectAndStore(ctx); err != nil {
				fmt.Printf("Modbus collection error: %v\n", err)
			}
		}
	}
}

func (c *Collector) CollectAndStore(ctx context.Context) error {
	now := time.Now()

	for tankID := 1; tankID <= c.cfg.TankCount; tankID++ {
		if err := c.collectTankData(ctx, tankID, now); err != nil {
			fmt.Printf("Tank %d collection error: %v\n", tankID, err)
			continue
		}
	}

	return nil
}

func (c *Collector) collectTankData(ctx context.Context, tankID int, timestamp time.Time) error {
	baseAddr := (tankID - 1) * 1000

	var tempData []models.TemperatureData
	for layer := 1; layer <= c.cfg.Layers; layer++ {
		for sensor := 0; sensor < c.cfg.ThermoPerLayer; sensor++ {
			addr := baseAddr + (layer-1)*c.cfg.ThermoPerLayer + sensor
			temp, err := c.readFloat(addr)
			if err != nil {
				continue
			}
			tempData = append(tempData, models.TemperatureData{
				Time:          timestamp,
				TankID:        tankID,
				Layer:         layer,
				SensorIndex:   sensor,
				Temperature:   temp,
				ModbusAddress: addr,
			})
		}
	}

	if len(tempData) > 0 {
		if err := c.db.InsertTemperatureData(ctx, tempData); err != nil {
			return fmt.Errorf("insert temp data: %w", err)
		}
		if err := c.calculateAndStoreLayerSummary(ctx, tankID, tempData, timestamp); err != nil {
			fmt.Printf("Layer summary error: %v\n", err)
		}
	}

	var densityData []models.DensityData
	heightPositions := []float64{4.0, 24.0, 44.0}
	for sensor := 0; sensor < c.cfg.DensityMeters; sensor++ {
		addr := baseAddr + 500 + sensor
		density, err := c.readFloat(addr)
		if err != nil {
			continue
		}
		densityData = append(densityData, models.DensityData{
			Time:           timestamp,
			TankID:         tankID,
			SensorIndex:    sensor,
			Density:        density,
			HeightPosition: heightPositions[sensor],
			ModbusAddress:  addr,
		})
	}

	if len(densityData) > 0 {
		if err := c.db.InsertDensityData(ctx, densityData); err != nil {
			return fmt.Errorf("insert density data: %w", err)
		}
	}

	pressureAddr := baseAddr + 600
	pressure, err := c.readFloat(pressureAddr)
	if err == nil {
		pressureData := []models.PressureData{{
			Time:          timestamp,
			TankID:        tankID,
			Pressure:      pressure,
			ModbusAddress: pressureAddr,
		}}
		if err := c.db.InsertPressureData(ctx, pressureData); err != nil {
			return fmt.Errorf("insert pressure data: %w", err)
		}
	}

	var bogData []models.BOGCompressorData
	for compID := 1; compID <= 2; compID++ {
		statusAddr := baseAddr + 700 + (compID-1)*10
		vibAddr := statusAddr + 1
		currentAddr := statusAddr + 2
		dischargeAddr := statusAddr + 3

		status, err := c.readUint16(statusAddr)
		if err != nil {
			continue
		}
		vib, _ := c.readFloat(vibAddr)
		current, _ := c.readFloat(currentAddr)
		discharge, _ := c.readFloat(dischargeAddr)

		bogData = append(bogData, models.BOGCompressorData{
			Time:              timestamp,
			TankID:            tankID,
			CompressorID:      compID,
			RunningStatus:     int(status),
			VibrationLevel:    vib,
			MotorCurrent:      current,
			DischargePressure: discharge,
			ModbusAddress:     statusAddr,
		})
	}

	if len(bogData) > 0 {
		if err := c.db.InsertBOGCompressorData(ctx, bogData); err != nil {
			return fmt.Errorf("insert BOG data: %w", err)
		}
	}

	return nil
}

func (c *Collector) calculateAndStoreLayerSummary(ctx context.Context, tankID int, tempData []models.TemperatureData, timestamp time.Time) error {
	layerTemps := make(map[int][]float64)
	for _, d := range tempData {
		layerTemps[d.Layer] = append(layerTemps[d.Layer], d.Temperature)
	}

	var summaries []models.LayerSummary
	for layer, temps := range layerTemps {
		if len(temps) == 0 {
			continue
		}
		avg, min, max, std := calculateStats(temps)
		summaries = append(summaries, models.LayerSummary{
			Time:    timestamp,
			TankID:  tankID,
			Layer:   layer,
			AvgTemp: avg,
			MinTemp: min,
			MaxTemp: max,
			TempStd: std,
		})
	}

	if len(summaries) > 0 {
		return c.db.InsertLayerSummary(ctx, summaries)
	}
	return nil
}

func calculateStats(data []float64) (avg, min, max, std float64) {
	if len(data) == 0 {
		return 0, 0, 0, 0
	}

	sum := 0.0
	min = data[0]
	max = data[0]
	for _, v := range data {
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	avg = sum / float64(len(data))

	if len(data) > 1 {
		variance := 0.0
		for _, v := range data {
			variance += math.Pow(v-avg, 2)
		}
		std = math.Sqrt(variance / float64(len(data)-1))
	}

	return avg, min, max, std
}

func (c *Collector) readFloat(address int) (float64, error) {
	results, err := c.client.ReadInputRegisters(uint16(address), 2)
	if err != nil {
		return 0, err
	}
	if len(results) < 4 {
		return 0, fmt.Errorf("insufficient data")
	}

	bits := uint32(results[0])<<16 | uint32(results[1])
	return math.Float32frombits(bits), nil
}

func (c *Collector) readUint16(address int) (uint16, error) {
	results, err := c.client.ReadInputRegisters(uint16(address), 1)
	if err != nil {
		return 0, err
	}
	if len(results) < 2 {
		return 0, fmt.Errorf("insufficient data")
	}
	return uint16(results[0])<<8 | uint16(results[1]), nil
}
