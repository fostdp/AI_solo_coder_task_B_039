package main

import (
	"context"
	"fmt"
	"lng-monitoring/alarm"
	"lng-monitoring/api"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/modbus"
	"lng-monitoring/prediction"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func main() {
	cfg := config.Load()

	db, err := database.New(&cfg.Database)
	if err != nil {
		fmt.Printf("Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Println("Database connected successfully")

	collector, err := modbus.NewCollector(&cfg.Modbus, db)
	if err != nil {
		fmt.Printf("Failed to create modbus collector: %v\n", err)
		os.Exit(1)
	}
	defer collector.Close()

	if err := collector.Connect(); err != nil {
		fmt.Printf("Failed to connect to modbus: %v\n", err)
		fmt.Println("Continuing without modbus connection (will retry)")
	} else {
		fmt.Println("Modbus connected successfully")
	}

	predictor := prediction.NewPredictor(&cfg.Prediction, db)
	fmt.Println("Rollover predictor initialized")

	alarmEngine := alarm.NewEngine(&cfg.Alarm, db, &cfg.OPCUA)
	fmt.Println("Alarm engine initialized")

	server := api.NewServer(cfg, db, predictor, alarmEngine)
	fmt.Println("API server initialized")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Starting Modbus data collection...")
		collector.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Starting rollover prediction service...")
		predictor.Start(ctx, cfg.Modbus.TankCount)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("Starting alarm monitoring service...")
		alarmEngine.Start(ctx, cfg.Modbus.TankCount)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Printf("Starting API server on %s:%d...\n", cfg.Server.Host, cfg.Server.Port)
		if err := server.Start(); err != nil {
			fmt.Printf("API server error: %v\n", err)
			cancel()
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nReceived shutdown signal, stopping services...")
	cancel()

	wg.Wait()
	fmt.Println("All services stopped gracefully")
}
