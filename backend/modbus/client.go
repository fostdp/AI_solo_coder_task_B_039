package modbus

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/models"
	"math"
	"sync"
	"time"

	"github.com/goburrow/modbus"
)

type Priority int

const (
	PriorityHigh   Priority = 3
	PriorityMedium Priority = 2
	PriorityLow    Priority = 1
)

type ModbusTask struct {
	ID         string
	Priority   Priority
	Interval   time.Duration
	LastRun    time.Time
	Execute    func(ctx context.Context) error
	Retries    int
	MaxRetries int
}

type PriorityQueue struct {
	tasks  []*ModbusTask
	mu     sync.Mutex
}

type Collector struct {
	cfg       *config.ModbusConfig
	db        *database.DB
	client    modbus.Client
	handler   *modbus.TCPClientHandler
	queue     *PriorityQueue
	highFreq  *time.Ticker
	lowFreq   *time.Ticker
}

func (pq *PriorityQueue) Push(task *ModbusTask) {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	pq.tasks = append(pq.tasks, task)
	pq.heapifyUp(len(pq.tasks) - 1)
}

func (pq *PriorityQueue) Pop() *ModbusTask {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	if len(pq.tasks) == 0 {
		return nil
	}
	task := pq.tasks[0]
	last := len(pq.tasks) - 1
	pq.tasks[0] = pq.tasks[last]
	pq.tasks = pq.tasks[:last]
	pq.heapifyDown(0)
	return task
}

func (pq *PriorityQueue) Len() int {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return len(pq.tasks)
}

func (pq *PriorityQueue) heapifyUp(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if pq.tasks[index].Priority > pq.tasks[parent].Priority {
			pq.tasks[index], pq.tasks[parent] = pq.tasks[parent], pq.tasks[index]
			index = parent
		} else {
			break
		}
	}
}

func (pq *PriorityQueue) heapifyDown(index int) {
	n := len(pq.tasks)
	for {
		left := 2*index + 1
		right := 2*index + 2
		largest := index
		if left < n && pq.tasks[left].Priority > pq.tasks[largest].Priority {
			largest = left
		}
		if right < n && pq.tasks[right].Priority > pq.tasks[largest].Priority {
			largest = right
		}
		if largest != index {
			pq.tasks[index], pq.tasks[largest] = pq.tasks[largest], pq.tasks[index]
			index = largest
		} else {
			break
		}
	}
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
		queue:   &PriorityQueue{},
	}, nil
}

func (c *Collector) Close() error {
	if c.highFreq != nil {
		c.highFreq.Stop()
	}
	if c.lowFreq != nil {
		c.lowFreq.Stop()
	}
	if c.handler != nil {
		return c.handler.Close()
	}
	return nil
}

func (c *Collector) Connect() error {
	return c.handler.Connect()
}

func (c *Collector) Start(ctx context.Context) {
	interval := time.Duration(c.cfg.IntervalSec) * time.Second
	c.highFreq = time.NewTicker(interval)
	c.lowFreq = time.NewTicker(interval * 2)
	defer c.highFreq.Stop()
	defer c.lowFreq.Stop()

	go c.processQueue(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.highFreq.C:
			c.enqueueHighPriorityTasks(ctx)
			c.enqueueMediumPriorityTasks(ctx)
		case <-c.lowFreq.C:
			c.enqueueLowPriorityTasks(ctx)
		}
	}
}

func (c *Collector) enqueueHighPriorityTasks(ctx context.Context) {
	now := time.Now()
	for tankID := 1; tankID <= c.cfg.TankCount; tankID++ {
		tank := tankID
		c.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("pressure_%d", tank),
			Priority:   PriorityHigh,
			Interval:   time.Duration(c.cfg.IntervalSec) * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) error { return c.collectPressureData(ctx, tank, now) },
			MaxRetries: 3,
		})
		c.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("bog_%d", tank),
			Priority:   PriorityHigh,
			Interval:   time.Duration(c.cfg.IntervalSec) * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) error { return c.collectBOGData(ctx, tank, now) },
			MaxRetries: 3,
		})
	}
}

func (c *Collector) enqueueMediumPriorityTasks(ctx context.Context) {
	now := time.Now()
	for tankID := 1; tankID <= c.cfg.TankCount; tankID++ {
		tank := tankID
		c.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("density_%d", tank),
			Priority:   PriorityMedium,
			Interval:   time.Duration(c.cfg.IntervalSec) * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) error { return c.collectDensityData(ctx, tank, now) },
			MaxRetries: 2,
		})
	}
}

func (c *Collector) enqueueLowPriorityTasks(ctx context.Context) {
	now := time.Now()
	for tankID := 1; tankID <= c.cfg.TankCount; tankID++ {
		tank := tankID
		c.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("temperature_%d", tank),
			Priority:   PriorityLow,
			Interval:   time.Duration(c.cfg.IntervalSec) * 2 * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) error { return c.collectTemperatureData(ctx, tank, now) },
			MaxRetries: 1,
		})
	}
}

func (c *Collector) processQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			task := c.queue.Pop()
			if task == nil {
				time.Sleep(10 * time.Millisecond)
				continue
			}

			if err := task.Execute(ctx); err != nil {
				fmt.Printf("Task %s error: %v\n", task.ID, err)
				if task.Retries < task.MaxRetries {
					task.Retries++
					time.AfterFunc(1*time.Second, func() {
						c.queue.Push(task)
					})
				}
			}
		}
	}
}

func (c *Collector) collectTemperatureData(ctx context.Context, tankID int, timestamp time.Time) error {
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
	return nil
}

func (c *Collector) collectDensityData(ctx context.Context, tankID int, timestamp time.Time) error {
	baseAddr := (tankID - 1) * 1000
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
		return c.db.InsertDensityData(ctx, densityData)
	}
	return nil
}

func (c *Collector) collectPressureData(ctx context.Context, tankID int, timestamp time.Time) error {
	baseAddr := (tankID - 1) * 1000
	pressureAddr := baseAddr + 600

	pressure, err := c.readFloat(pressureAddr)
	if err != nil {
		return fmt.Errorf("read pressure: %w", err)
	}

	pressureData := []models.PressureData{{
		Time:          timestamp,
		TankID:        tankID,
		Pressure:      pressure,
		ModbusAddress: pressureAddr,
	}}
	return c.db.InsertPressureData(ctx, pressureData)
}

func (c *Collector) collectBOGData(ctx context.Context, tankID int, timestamp time.Time) error {
	baseAddr := (tankID - 1) * 1000
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
		return c.db.InsertBOGCompressorData(ctx, bogData)
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
