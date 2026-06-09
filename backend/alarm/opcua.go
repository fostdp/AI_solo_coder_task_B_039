package alarm

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/models"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

type OPCUAClient struct {
	cfg    *config.OPCUAConfig
	client *opcua.Client
	ctx    context.Context
}

func NewOPCUAClient(cfg *config.OPCUAConfig) *OPCUAClient {
	return &OPCUAClient{
		cfg: cfg,
		ctx: context.Background(),
	}
}

func (c *OPCUAClient) Connect() error {
	endpoints, err := opcua.GetEndpoints(c.ctx, c.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("get endpoints: %w", err)
	}

	if len(endpoints) == 0 {
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
		opcua.AutoReconnect(true),
		opcua.ReconnectInterval(5000),
	}

	client, err := opcua.NewClient(ep.EndpointURL, opts...)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	if err := client.Connect(c.ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	c.client = client
	return nil
}

func (c *OPCUAClient) Close() error {
	if c.client != nil {
		return c.client.Close(c.ctx)
	}
	return nil
}

func (c *OPCUAClient) PushAlarm(alarm *models.Alarm) error {
	if c.client == nil {
		if err := c.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
	}

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
	if c.client == nil {
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
	if c.client == nil {
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
