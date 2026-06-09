package modbus_poller

import (
	"context"
	"fmt"
	"lng-monitoring/config"
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
	Execute    func(ctx context.Context) (interface{}, error)
	Retries    int
	MaxRetries int
}

type PollResult struct {
	TaskID      string
	TankID      int
	DataType    string
	Data        interface{}
	CollectedAt time.Time
	Error       error
}

type PriorityQueue struct {
	tasks []*ModbusTask
	mu    sync.Mutex
}

type ModbusPoller struct {
	cfg           *config.ModbusConfig
	client        modbus.Client
	handler       *modbus.TCPClientHandler
	queue         *PriorityQueue
	highFreq      *time.Ticker
	lowFreq       *time.Ticker
	resultChannel chan<- PollResult
	mu            sync.Mutex
}

func NewPoller(cfg *config.ModbusConfig, resultChan chan<- PollResult) (*ModbusPoller, error) {
	handler := modbus.NewTCPClientHandler(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	handler.SlaveId = cfg.SlaveID
	handler.Timeout = 5 * time.Second

	client := modbus.NewClient(handler)

	return &ModbusPoller{
		cfg:           cfg,
		client:        client,
		handler:       handler,
		queue:         &PriorityQueue{},
		resultChannel: resultChan,
	}, nil
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

func (p *ModbusPoller) Close() error {
	if p.highFreq != nil {
		p.highFreq.Stop()
	}
	if p.lowFreq != nil {
		p.lowFreq.Stop()
	}
	if p.handler != nil {
		return p.handler.Close()
	}
	return nil
}

func (p *ModbusPoller) Connect() error {
	return p.handler.Connect()
}

func (p *ModbusPoller) Start(ctx context.Context) {
	interval := time.Duration(p.cfg.IntervalSec) * time.Second
	p.highFreq = time.NewTicker(interval)
	p.lowFreq = time.NewTicker(interval * 2)
	defer p.highFreq.Stop()
	defer p.lowFreq.Stop()

	go p.processQueue(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.highFreq.C:
			p.enqueueHighPriorityTasks(ctx)
			p.enqueueMediumPriorityTasks(ctx)
		case <-p.lowFreq.C:
			p.enqueueLowPriorityTasks(ctx)
		}
	}
}

func (p *ModbusPoller) enqueueHighPriorityTasks(ctx context.Context) {
	now := time.Now()
	for tankID := 1; tankID <= p.cfg.TankCount; tankID++ {
		tank := tankID
		p.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("pressure_%d", tank),
			Priority:   PriorityHigh,
			Interval:   time.Duration(p.cfg.IntervalSec) * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) (interface{}, error) { return p.collectPressureData(tank, now) },
			MaxRetries: 3,
		})
		p.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("bog_%d", tank),
			Priority:   PriorityHigh,
			Interval:   time.Duration(p.cfg.IntervalSec) * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) (interface{}, error) { return p.collectBOGData(tank, now) },
			MaxRetries: 3,
		})
	}
}

func (p *ModbusPoller) enqueueMediumPriorityTasks(ctx context.Context) {
	now := time.Now()
	for tankID := 1; tankID <= p.cfg.TankCount; tankID++ {
		tank := tankID
		p.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("density_%d", tank),
			Priority:   PriorityMedium,
			Interval:   time.Duration(p.cfg.IntervalSec) * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) (interface{}, error) { return p.collectDensityData(tank, now) },
			MaxRetries: 2,
		})
	}
}

func (p *ModbusPoller) enqueueLowPriorityTasks(ctx context.Context) {
	now := time.Now()
	for tankID := 1; tankID <= p.cfg.TankCount; tankID++ {
		tank := tankID
		p.queue.Push(&ModbusTask{
			ID:         fmt.Sprintf("temperature_%d", tank),
			Priority:   PriorityLow,
			Interval:   time.Duration(p.cfg.IntervalSec) * 2 * time.Second,
			LastRun:    now,
			Execute:    func(ctx context.Context) (interface{}, error) { return p.collectTemperatureData(tank, now) },
			MaxRetries: 1,
		})
	}
}

func (p *ModbusPoller) processQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			task := p.queue.Pop()
			if task == nil {
				time.Sleep(10 * time.Millisecond)
				continue
			}

			tankID := p.extractTankID(task.ID)
			dataType := p.extractDataType(task.ID)

			result, err := task.Execute(ctx)
			pollResult := PollResult{
				TaskID:      task.ID,
				TankID:      tankID,
				DataType:    dataType,
				Data:        result,
				CollectedAt: time.Now(),
				Error:       err,
			}

			if err != nil {
				fmt.Printf("Task %s error: %v\n", task.ID, err)
				if task.Retries < task.MaxRetries {
					task.Retries++
					time.AfterFunc(1*time.Second, func() {
						p.queue.Push(task)
					})
				}
			}

			select {
			case p.resultChannel <- pollResult:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (p *ModbusPoller) extractTankID(taskID string) int {
	for i := len(taskID) - 1; i >= 0; i-- {
		if taskID[i] == '_' {
			id := 0
			for j := i + 1; j < len(taskID); j++ {
				id = id*10 + int(taskID[j]-'0')
			}
			return id
		}
	}
	return 1
}

func (p *ModbusPoller) extractDataType(taskID string) string {
	for i := 0; i < len(taskID); i++ {
		if taskID[i] == '_' {
			return taskID[:i]
		}
	}
	return "unknown"
}

func (p *ModbusPoller) readFloat(addr uint16) (float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	results, err := p.client.ReadHoldingRegisters(addr, 2)
	if err != nil {
		return 0, err
	}
	if len(results) < 4 {
		return 0, fmt.Errorf("insufficient data")
	}

	bits := uint32(results[0])<<16 | uint32(results[1])
	f := *(*float32)(&bits)
	return float64(f), nil
}

func (p *ModbusPoller) readUint16(addr uint16) (uint16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	results, err := p.client.ReadHoldingRegisters(addr, 1)
	if err != nil {
		return 0, err
	}
	if len(results) < 2 {
		return 0, fmt.Errorf("insufficient data")
	}
	return results[0], nil
}

func (p *ModbusPoller) WriteSingleRegister(addr uint16, value uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, err := p.client.WriteSingleRegister(addr, value)
	return err
}

func (p *ModbusPoller) collectTemperatureData(tankID int, timestamp time.Time) (interface{}, error) {
	baseAddr := (tankID - 1) * 1000
	var tempData []models.TemperatureData

	for layer := 1; layer <= p.cfg.Layers; layer++ {
		for sensor := 0; sensor < p.cfg.ThermoPerLayer; sensor++ {
			addr := baseAddr + (layer-1)*p.cfg.ThermoPerLayer + sensor
			temp, err := p.readFloat(uint16(addr))
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

	return tempData, nil
}

func (p *ModbusPoller) collectDensityData(tankID int, timestamp time.Time) (interface{}, error) {
	baseAddr := (tankID - 1) * 1000
	var densityData []models.DensityData
	heightPositions := []float64{4.0, 24.0, 44.0}

	for sensor := 0; sensor < p.cfg.DensityMeters; sensor++ {
		addr := baseAddr + 500 + sensor
		density, err := p.readFloat(uint16(addr))
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

	return densityData, nil
}

func (p *ModbusPoller) collectPressureData(tankID int, timestamp time.Time) (interface{}, error) {
	baseAddr := (tankID - 1) * 1000
	pressureAddr := baseAddr + 600

	pressure, err := p.readFloat(uint16(pressureAddr))
	if err != nil {
		return nil, fmt.Errorf("read pressure: %w", err)
	}

	pressureData := []models.PressureData{{
		Time:          timestamp,
		TankID:        tankID,
		Pressure:      pressure,
		ModbusAddress: pressureAddr,
	}}
	return pressureData, nil
}

func (p *ModbusPoller) collectBOGData(tankID int, timestamp time.Time) (interface{}, error) {
	baseAddr := (tankID - 1) * 1000
	var bogData []models.BOGCompressorData

	for compID := 1; compID <= 2; compID++ {
		statusAddr := baseAddr + 700 + (compID-1)*10
		vibAddr := statusAddr + 1
		currentAddr := statusAddr + 2
		dischargeAddr := statusAddr + 3

		status, err := p.readUint16(uint16(statusAddr))
		if err != nil {
			continue
		}
		vib, _ := p.readFloat(uint16(vibAddr))
		current, _ := p.readFloat(uint16(currentAddr))
		discharge, _ := p.readFloat(uint16(dischargeAddr))

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

	return bogData, nil
}

func (p *ModbusPoller) CalculateLayerSummary(tempData []models.TemperatureData, tankID int, timestamp time.Time) []models.LayerSummary {
	layerMap := make(map[int][]float64)
	for _, d := range tempData {
		layerMap[d.Layer] = append(layerMap[d.Layer], d.Temperature)
	}

	var summaries []models.LayerSummary
	for layer, temps := range layerMap {
		if len(temps) == 0 {
			continue
		}

		sum := 0.0
		minT := temps[0]
		maxT := temps[0]
		for _, t := range temps {
			sum += t
			if t < minT {
				minT = t
			}
			if t > maxT {
				maxT = t
			}
		}
		avg := sum / float64(len(temps))

		variance := 0.0
		for _, t := range temps {
			variance += (t - avg) * (t - avg)
		}
		stddev := math.Sqrt(variance / float64(len(temps)))

		summaries = append(summaries, models.LayerSummary{
			Time:    timestamp,
			TankID:  tankID,
			Layer:   layer,
			AvgTemp: avg,
			MinTemp: minT,
			MaxTemp: maxT,
			TempStd: stddev,
		})
	}

	return summaries
}
