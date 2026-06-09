package alarm

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/models"
	"sync"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

type OPCUAClient struct {
	cfg          *config.OPCUAConfig
	client       *opcua.Client
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	connected    bool
	heartbeatTicker *time.Ticker
	alarmBuffer  []*models.Alarm
	bufferMu     sync.Mutex
	maxBufferSize int
	reconnectCount int
	lastConnectAttempt time.Time
	onConnect    func()
	onDisconnect func(error)
}

func NewOPCUAClient(cfg *config.OPCUAConfig) *OPCUAClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &OPCUAClient{
		cfg:          cfg,
		ctx:          ctx,
		cancel:       cancel,
		maxBufferSize: 100,
	}
}

func (c *OPCUAClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if c.connected && c.client != nil {
		return nil
	}

	if now.Sub(c.lastConnectAttempt) < 5*time.Second && c.reconnectCount > 0 {
		delay := time.Duration(c.reconnectCount) * 2 * time.Second
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		if now.Sub(c.lastConnectAttempt) < delay {
			return fmt.Errorf("reconnect throttled, next attempt in %v", delay-now.Sub(c.lastConnectAttempt))
		}
	}

	c.lastConnectAttempt = now
	c.reconnectCount++

	if c.client != nil {
		_ = c.client.Close(c.ctx)
		c.client = nil
	}

	endpoints, err := opcua.GetEndpoints(c.ctx, c.cfg.Endpoint)
	if err != nil {
		c.handleConnectionLost(fmt.Errorf("get endpoints: %w", err))
		return fmt.Errorf("get endpoints: %w", err)
	}

	if len(endpoints) == 0 {
		c.handleConnectionLost(fmt.Errorf("no endpoints found"))
		return fmt.Errorf("no endpoints found")
	}

	ep := endpoints[0]
	for _, e := range endpoints {
		if e.SecurityPolicyURI == ua.SecurityPolicyURINone && e.SecurityMode == ua.MessageSecurityModeNone {
			ep = e
			break
		}
	}

	opts := []opcua.Option{
		opcua.SecurityPolicy(ua.SecurityPolicyURINone),
		opcua.SecurityMode(ua.MessageSecurityModeNone),
		opcua.AutoReconnect(false),
		opcua.ReconnectInterval(1000),
		opcua.SessionTimeout(30 * time.Second),
	}

	client, err := opcua.NewClient(ep.EndpointURL, opts...)
	if err != nil {
		c.handleConnectionLost(fmt.Errorf("create client: %w", err))
		return fmt.Errorf("create client: %w", err)
	}

	if err := client.Connect(c.ctx); err != nil {
		c.handleConnectionLost(fmt.Errorf("connect: %w", err))
		return fmt.Errorf("connect: %w", err)
	}

	c.client = client
	c.connected = true
	c.reconnectCount = 0

	if c.onConnect != nil {
		c.onConnect()
	}

	go c.flushBufferedAlarms()

	fmt.Println("OPC UA connection established successfully")
	return nil
}

func (c *OPCUAClient) handleConnectionLost(err error) {
	c.connected = false
	if c.onDisconnect != nil {
		c.onDisconnect(err)
	}
	fmt.Printf("OPC UA connection lost: %v, reconnect count: %d\n", err, c.reconnectCount)
}

func (c *OPCUAClient) StartHeartbeat(ctx context.Context) {
	if c.heartbeatTicker != nil {
		c.heartbeatTicker.Stop()
	}
	c.heartbeatTicker = time.NewTicker(10 * time.Second)

	go func() {
		for {
			select {
			case <-ctx.Done():
				if c.heartbeatTicker != nil {
					c.heartbeatTicker.Stop()
				}
				return
			case <-c.heartbeatTicker.C:
				if err := c.checkConnection(); err != nil {
					fmt.Printf("OPC UA heartbeat failed: %v, attempting reconnect...\n", err)
					c.handleConnectionLost(err)
					go c.tryReconnect()
				}
			}
		}
	}()
}

func (c *OPCUAClient) checkConnection() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil || !c.connected {
		return fmt.Errorf("not connected")
	}

	nodeID, err := ua.ParseNodeID("i=2258")
	if err != nil {
		return fmt.Errorf("parse node id: %w", err)
	}

	req := &ua.ReadRequest{
		NodesToRead: []*ua.ReadValueID{
			{
				NodeID:      nodeID,
				AttributeID: ua.AttributeIDValue,
			},
		},
	}

	resp, err := c.client.Read(c.ctx, req)
	if err != nil {
		return fmt.Errorf("heartbeat read: %w", err)
	}

	if len(resp.Results) == 0 || resp.Results[0].Status != ua.StatusOK {
		return fmt.Errorf("heartbeat status: %v", resp.Results[0].Status)
	}

	return nil
}

func (c *OPCUAClient) tryReconnect() {
	maxAttempts := 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		delay := time.Duration(attempt+1) * 3 * time.Second
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		time.Sleep(delay)

		if err := c.Connect(); err == nil {
			return
		}
		fmt.Printf("OPC UA reconnect attempt %d/%d failed\n", attempt+1, maxAttempts)
	}
	fmt.Println("OPC UA max reconnect attempts reached, will retry on next heartbeat")
}

func (c *OPCUAClient) Close() error {
	c.cancel()
	if c.heartbeatTicker != nil {
		c.heartbeatTicker.Stop()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		return c.client.Close(c.ctx)
	}
	return nil
}

func (c *OPCUAClient) bufferAlarm(alarm *models.Alarm) {
	c.bufferMu.Lock()
	defer c.bufferMu.Unlock()

	if len(c.alarmBuffer) >= c.maxBufferSize {
		c.alarmBuffer = c.alarmBuffer[1:]
	}
	c.alarmBuffer = append(c.alarmBuffer, alarm)
	fmt.Printf("Alarm buffered (total: %d): %v\n", len(c.alarmBuffer), alarm.AlarmMessage)
}

func (c *OPCUAClient) flushBufferedAlarms() {
	c.bufferMu.Lock()
	alarms := make([]*models.Alarm, len(c.alarmBuffer))
	copy(alarms, c.alarmBuffer)
	c.alarmBuffer = nil
	c.bufferMu.Unlock()

	if len(alarms) == 0 {
		return
	}

	fmt.Printf("Flushing %d buffered alarms...\n", len(alarms))
	for _, alarm := range alarms {
		if err := c.pushAlarmInternal(alarm); err != nil {
			fmt.Printf("Failed to flush alarm %d: %v, re-buffering\n", alarm.AlarmID, err)
			c.bufferAlarm(alarm)
		} else {
			fmt.Printf("Flushed alarm %d successfully\n", alarm.AlarmID)
		}
	}
}

func (c *OPCUAClient) PushAlarm(alarm *models.Alarm) error {
	c.mu.Lock()
	isConnected := c.connected && c.client != nil
	c.mu.Unlock()

	if !isConnected {
		c.bufferAlarm(alarm)
		go c.tryReconnect()
		return fmt.Errorf("opcua not connected, alarm buffered")
	}

	if err := c.pushAlarmInternal(alarm); err != nil {
		c.bufferAlarm(alarm)
		c.handleConnectionLost(err)
		go c.tryReconnect()
		return fmt.Errorf("push failed: %w, alarm buffered", err)
	}

	return nil
}

func (c *OPCUAClient) pushAlarmInternal(alarm *models.Alarm) error {
	nodeID, err := ua.ParseNodeID(c.cfg.NodeID)
	if err != nil {
		return fmt.Errorf("parse node id: %w", err)
	}

	alarmData := map[string]interface{}{
		"alarm_id":       alarm.AlarmID,
		"time":           alarm.Time.Format("2006-01-02 15:04:05"),
		"tank_id":        alarm.TankID,
		"alarm_level":    alarm.AlarmLevel,
		"alarm_type":     alarm.AlarmType,
		"alarm_message":  alarm.AlarmMessage,
		"threshold_value": alarm.ThresholdValue,
		"actual_value":   alarm.ActualValue,
	}

	variant, err := ua.NewVariant(alarmData)
	if err != nil {
		return fmt.Errorf("create variant: %w", err)
	}

	req := &ua.WriteRequest{
		NodesToWrite: []*ua.WriteValue{
			{
				NodeID:      nodeID,
				AttributeID: ua.AttributeIDValue,
				Value: &ua.DataValue{
					EncodingMask: ua.DataValueValue,
					Value:        variant,
				},
			},
		},
	}

	resp, err := c.client.Write(c.ctx, req)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}

	for _, res := range resp.Results {
		if res != ua.StatusOK {
			return fmt.Errorf("opcua write status: %v", res)
		}
	}

	return nil
}

func (c *OPCUAClient) WriteValue(nodeIDStr string, value interface{}) error {
	c.mu.Lock()
	isConnected := c.connected && c.client != nil
	c.mu.Unlock()

	if !isConnected {
		if err := c.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
	}

	nodeID, err := ua.ParseNodeID(nodeIDStr)
	if err != nil {
		return fmt.Errorf("parse node id: %w", err)
	}

	variant, err := ua.NewVariant(value)
	if err != nil {
		return fmt.Errorf("create variant: %w", err)
	}

	req := &ua.WriteRequest{
		NodesToWrite: []*ua.WriteValue{
			{
				NodeID:      nodeID,
				AttributeID: ua.AttributeIDValue,
				Value: &ua.DataValue{
					EncodingMask: ua.DataValueValue,
					Value:        variant,
				},
			},
		},
	}

	resp, err := c.client.Write(c.ctx, req)
	if err != nil {
		c.handleConnectionLost(err)
		return fmt.Errorf("write: %w", err)
	}

	for _, res := range resp.Results {
		if res != ua.StatusOK {
			return fmt.Errorf("opcua write status: %v", res)
		}
	}

	return nil
}

func (c *OPCUAClient) ReadValue(nodeIDStr string) (interface{}, error) {
	c.mu.Lock()
	isConnected := c.connected && c.client != nil
	c.mu.Unlock()

	if !isConnected {
		if err := c.Connect(); err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
	}

	nodeID, err := ua.ParseNodeID(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse node id: %w", err)
	}

	req := &ua.ReadRequest{
		NodesToRead: []*ua.ReadValueID{
			{
				NodeID:      nodeID,
				AttributeID: ua.AttributeIDValue,
			},
		},
	}

	resp, err := c.client.Read(c.ctx, req)
	if err != nil {
		c.handleConnectionLost(err)
		return nil, fmt.Errorf("read: %w", err)
	}

	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("no results")
	}

	result := resp.Results[0]
	if result.Status != ua.StatusOK {
		return nil, fmt.Errorf("read status: %v", result.Status)
	}

	return result.Value.Value(), nil
}

func (c *OPCUAClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected && c.client != nil
}

func (c *OPCUAClient) GetBufferSize() int {
	c.bufferMu.Lock()
	defer c.bufferMu.Unlock()
	return len(c.alarmBuffer)
}
