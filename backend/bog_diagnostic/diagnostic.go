package bog_diagnostic

import (
	"context"
	"fmt"
	"lng-monitoring/config"
	"lng-monitoring/database"
	"lng-monitoring/messages"
	"lng-monitoring/models"
	"math"
	"math/rand"
	"sync"
	"time"
)

type BOGDiagnosticService struct {
	cfg         *config.Config
	db          *database.DB
	requestChan <-chan messages.BOGBatch
	resultChan  chan<- messages.BOGDiagnosticResult
	mu          sync.RWMutex
	iforest     *IsolationForest
	modelParams *config.BOGDiagnosticParams
}

type IsolationForest struct {
	trees      []*IsolationTree
	nTrees     int
	sampleSize int
	maxDepth   int
	mu         sync.RWMutex
}

type IsolationTree struct {
	root           *TreeNode
	sampleFeatures [][]float64
	heightLimit    int
}

type TreeNode struct {
	splitFeature int
	splitValue   float64
	left         *TreeNode
	right        *TreeNode
	isLeaf       bool
	size         int
}

func NewBOGDiagnosticService(
	cfg *config.Config,
	db *database.DB,
	requestChan <-chan messages.BOGBatch,
	resultChan chan<- messages.BOGDiagnosticResult,
) *BOGDiagnosticService {
	params := &config.BOGDiagnosticParams{
		ContaminationRate:     0.1,
		NormalVibrationRange:  [2]float64{0.5, 3.0},
		NormalCurrentRange:    [2]float64{15.0, 45.0},
		AnomalyThreshold:      cfg.BOGDiagnostic.AnomalyThreshold,
		WarningThreshold:      cfg.BOGDiagnostic.WarningThreshold,
		HistoryWindowHours:    cfg.BOGDiagnostic.HistoryWindowHours,
		TrendWindowPoints:     50,
		IForestNTrees:         100,
		IForestSampleSize:     256,
		FaultTypeThresholds: map[string]float64{
			"bearing_fault":      0.65,
			"piston_ring_wear":   0.60,
			"imbalance":          0.55,
			"motor_fault":        0.70,
		},
	}
	if cfg.ModelParams != nil {
		params = &cfg.ModelParams.BOGDiagnostic
	}

	return &BOGDiagnosticService{
		cfg:         cfg,
		db:          db,
		requestChan: requestChan,
		resultChan:  resultChan,
		iforest:     NewIsolationForest(params.IForestNTrees, params.IForestSampleSize),
		modelParams: params,
	}
}

func NewIsolationForest(nTrees, sampleSize int) *IsolationForest {
	return &IsolationForest{
		trees:      make([]*IsolationTree, nTrees),
		nTrees:     nTrees,
		sampleSize: sampleSize,
		maxDepth:   int(math.Ceil(math.Log2(float64(sampleSize)))),
	}
}

func (f *IsolationForest) Fit(X [][]float64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	nSamples := len(X)
	for i := 0; i < f.nTrees; i++ {
		sample := f.bootstrapSample(X, nSamples)
		f.trees[i] = f.buildTree(sample, 0)
	}
}

func (f *IsolationForest) bootstrapSample(X [][]float64, nSamples int) [][]float64 {
	sample := make([][]float64, f.sampleSize)
	for i := 0; i < f.sampleSize; i++ {
		idx := rand.Intn(nSamples)
		sample[i] = make([]float64, len(X[idx]))
		copy(sample[i], X[idx])
	}
	return sample
}

func (f *IsolationForest) buildTree(X [][]float64, currentDepth int) *IsolationTree {
	if currentDepth >= f.maxDepth || len(X) <= 1 || f.allSame(X) {
		return &TreeNode{
			isLeaf: true,
			size:   len(X),
		}
	}

	nFeatures := len(X[0])
	splitFeature := rand.Intn(nFeatures)

	minVal, maxVal := f.featureRange(X, splitFeature)
	if maxVal == minVal {
		return &TreeNode{
			isLeaf: true,
			size:   len(X),
		}
	}

	splitValue := minVal + rand.Float64()*(maxVal-minVal)

	var leftX, rightX [][]float64
	for _, x := range X {
		if x[splitFeature] < splitValue {
			leftX = append(leftX, x)
		} else {
			rightX = append(rightX, x)
		}
	}

	return &TreeNode{
		splitFeature: splitFeature,
		splitValue:   splitValue,
		left:         f.buildTree(leftX, currentDepth+1),
		right:        f.buildTree(rightX, currentDepth+1),
		isLeaf:       false,
		size:         len(X),
	}
}

func (f *IsolationForest) allSame(X [][]float64) bool {
	if len(X) <= 1 {
		return true
	}
	first := X[0]
	for _, x := range X[1:] {
		for j := range x {
			if x[j] != first[j] {
				return false
			}
		}
	}
	return true
}

func (f *IsolationForest) featureRange(X [][]float64, feature int) (float64, float64) {
	minVal := X[0][feature]
	maxVal := X[0][feature]
	for _, x := range X[1:] {
		if x[feature] < minVal {
			minVal = x[feature]
		}
		if x[feature] > maxVal {
			maxVal = x[feature]
		}
	}
	return minVal, maxVal
}

func (f *IsolationForest) AnomalyScore(x []float64) float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.trees[0] == nil {
		return 0.5
	}

	var totalDepth float64
	for _, tree := range f.trees {
		totalDepth += f.pathLength(x, tree.root, 0)
	}

	avgPathLength := totalDepth / float64(f.nTrees)
	cN := f.harmonicNumber(float64(f.sampleSize) - 1)
	normalizedLength := avgPathLength / cN

	score := math.Pow(2, -normalizedLength)
	return score
}

func (f *IsolationForest) pathLength(x []float64, node *TreeNode, currentDepth int) float64 {
	if node.isLeaf {
		if node.size <= 1 {
			return float64(currentDepth)
		}
		return float64(currentDepth) + f.harmonicNumber(float64(node.size)-1)
	}

	if x[node.splitFeature] < node.splitValue {
		return f.pathLength(x, node.left, currentDepth+1)
	}
	return f.pathLength(x, node.right, currentDepth+1)
}

func (f *IsolationForest) harmonicNumber(n float64) float64 {
	return math.Log(n) + 0.5772156649
}

func (s *BOGDiagnosticService) Start(ctx context.Context) {
	go s.processLoop(ctx)
	if s.cfg.BOGDiagnostic.AutoDiagnose {
		go s.scheduledDiagnostics(ctx)
	}
}

func (s *BOGDiagnosticService) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-s.requestChan:
			go s.processBatch(ctx, batch)
		}
	}
}

func (s *BOGDiagnosticService) scheduledDiagnostics(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.BOGDiagnostic.IntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDiagnosticsForAllTanks(ctx)
		}
	}
}

func (s *BOGDiagnosticService) processBatch(ctx context.Context, batch messages.BOGBatch) {
	s.ensureModelTrained(ctx, batch.TankID)

	for _, bogData := range batch.Data {
		if bogData.RunningStatus != 1 {
			continue
		}

		result := s.diagnoseCompressor(ctx, batch.TankID, bogData, batch.Data)
		select {
		case <-ctx.Done():
			return
		case s.resultChan <- result:
		}

		go s.saveDiagnostic(ctx, result)
	}
}

func (s *BOGDiagnosticService) runDiagnosticsForAllTanks(ctx context.Context) {
	for tankID := 1; tankID <= s.cfg.Modbus.TankCount; tankID++ {
		history, err := s.db.GetBOGHistory(ctx, tankID, s.modelParams.HistoryWindowHours)
		if err != nil || len(history) == 0 {
			continue
		}

		s.ensureModelTrainedWithData(ctx, tankID, history)

		compressorMap := make(map[int][]models.BOGCompressorData)
		for _, d := range history {
			compressorMap[d.CompressorID] = append(compressorMap[d.CompressorID], d)
		}

		for compID, compHistory := range compressorMap {
			if len(compHistory) == 0 {
				continue
			}
			latest := compHistory[len(compHistory)-1]
			if latest.RunningStatus != 1 {
				continue
			}

			result := s.diagnoseCompressor(ctx, tankID, latest, compHistory)
			select {
			case <-ctx.Done():
				return
			case s.resultChan <- result:
			}

			go s.saveDiagnostic(ctx, result)
		}
	}
}

func (s *BOGDiagnosticService) diagnoseCompressor(
	ctx context.Context,
	tankID int,
	data models.BOGCompressorData,
	history []models.BOGCompressorData,
) messages.BOGDiagnosticResult {
	features := s.extractFeatures(data, history)
	anomalyScore := s.iforest.AnomalyScore(features)

	vibTrend := s.calculateTrend(history, "vibration")
	currTrend := s.calculateTrend(history, "current")

	anomalyType := s.classifyFaultType(features, anomalyScore, vibTrend, currTrend)
	isAnomaly := anomalyScore > s.modelParams.AnomalyThreshold
	confidence := s.calculateConfidence(anomalyScore, features)
	remainingHours := s.estimateRemainingLife(anomalyScore, vibTrend, currTrend)
	recommendation := s.generateRecommendation(isAnomaly, anomalyType, anomalyScore, remainingHours)

	return messages.BOGDiagnosticResult{
		TankID:         tankID,
		CompressorID:   data.CompressorID,
		AnomalyScore:   anomalyScore,
		IsAnomaly:      isAnomaly,
		AnomalyType:    anomalyType,
		Confidence:     confidence,
		RemainingHours: remainingHours,
		Recommendation: recommendation,
		DiagnosedAt:    time.Now(),
	}
}

func (s *BOGDiagnosticService) extractFeatures(
	data models.BOGCompressorData,
	history []models.BOGCompressorData,
) []float64 {
	features := make([]float64, 8)

	features[0] = data.VibrationLevel
	features[1] = data.MotorCurrent
	features[2] = data.DischargePressure

	vibNorm := s.normalize(data.VibrationLevel, s.modelParams.NormalVibrationRange[0], s.modelParams.NormalVibrationRange[1])
	currNorm := s.normalize(data.MotorCurrent, s.modelParams.NormalCurrentRange[0], s.modelParams.NormalCurrentRange[1])
	features[3] = vibNorm
	features[4] = currNorm

	if len(history) >= 10 {
		n := s.modelParams.TrendWindowPoints
		if n > len(history) {
			n = len(history)
		}
		recent := history[len(history)-n:]

		var vibStd, currStd float64
		var vibMean, currMean float64
		for _, d := range recent {
			vibMean += d.VibrationLevel
			currMean += d.MotorCurrent
		}
		vibMean /= float64(n)
		currMean /= float64(n)

		for _, d := range recent {
			vibStd += math.Pow(d.VibrationLevel-vibMean, 2)
			currStd += math.Pow(d.MotorCurrent-currMean, 2)
		}
		features[5] = math.Sqrt(vibStd / float64(n-1))
		features[6] = math.Sqrt(currStd / float64(n-1))

		features[7] = vibNorm*0.6 + currNorm*0.4
	}

	return features
}

func (s *BOGDiagnosticService) normalize(value, min, max float64) float64 {
	rangeVal := max - min
	if rangeVal == 0 {
		return 0.5
	}
	return (value - min) / rangeVal
}

func (s *BOGDiagnosticService) calculateTrend(history []models.BOGCompressorData, metric string) float64 {
	n := s.modelParams.TrendWindowPoints
	if n > len(history) {
		n = len(history)
	}
	if n < 2 {
		return 0
	}

	recent := history[len(history)-n:]
	var sumX, sumY, sumXY, sumX2 float64

	for i, d := range recent {
		x := float64(i)
		var y float64
		switch metric {
		case "vibration":
			y = d.VibrationLevel
		case "current":
			y = d.MotorCurrent
		}
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	slope := (float64(n)*sumXY - sumX*sumY) / (float64(n)*sumX2 - sumX*sumX)
	return slope
}

func (s *BOGDiagnosticService) classifyFaultType(
	features []float64,
	anomalyScore float64,
	vibTrend, currTrend float64,
) string {
	if anomalyScore < s.modelParams.WarningThreshold {
		return "normal"
	}

	vibLevel := features[0]
	currLevel := features[1]
	vibNorm := features[3]
	currNorm := features[4]
	vibStd := features[5]
	currStd := features[6]

	if vibNorm > s.modelParams.FaultTypeThresholds["bearing_fault"] && vibStd > 2.0 && vibTrend > 0.01 {
		return "bearing_fault"
	}

	if vibNorm > s.modelParams.FaultTypeThresholds["piston_ring_wear"] &&
		currNorm > 0.3 && currTrend > 0.005 {
		return "piston_ring_wear"
	}

	if vibNorm > s.modelParams.FaultTypeThresholds["imbalance"] &&
		math.Abs(vibTrend) < 0.005 && vibStd > 1.5 {
		return "imbalance"
	}

	if currNorm > s.modelParams.FaultTypeThresholds["motor_fault"] &&
		currStd > 5.0 && vibLevel < 2.0 {
		return "motor_fault"
	}

	if anomalyScore > s.modelParams.AnomalyThreshold {
		return "unknown_anomaly"
	}

	return "normal"
}

func (s *BOGDiagnosticService) calculateConfidence(
	anomalyScore float64,
	features []float64,
) float64 {
	baseConf := math.Min(1.0, anomalyScore*1.2)
	featureSpread := 0.0
	for _, f := range features {
		featureSpread += math.Abs(f - 0.5)
	}
	featureSpread /= float64(len(features))

	confidence := baseConf*0.7 + featureSpread*0.3
	return math.Min(0.99, math.Max(0.01, confidence))
}

func (s *BOGDiagnosticService) estimateRemainingLife(
	anomalyScore float64,
	vibTrend, currTrend float64,
) float64 {
	if anomalyScore < s.modelParams.WarningThreshold {
		return math.Inf(1)
	}

	baseHours := 1000.0 * math.Exp(-3.0*(anomalyScore-s.modelParams.WarningThreshold))

	trendFactor := 1.0
	if vibTrend > 0 {
		trendFactor *= math.Exp(-50 * vibTrend)
	}
	if currTrend > 0 {
		trendFactor *= math.Exp(-100 * currTrend)
	}

	remaining := baseHours * trendFactor
	return math.Max(0, math.Min(8760, remaining))
}

func (s *BOGDiagnosticService) generateRecommendation(
	isAnomaly bool,
	anomalyType string,
	anomalyScore float64,
	remainingHours float64,
) string {
	if !isAnomaly {
		if anomalyScore > s.modelParams.WarningThreshold {
			return "运行状态接近警戒值，建议加强监测，缩短巡检周期至4小时"
		}
		return "压缩机运行状态正常，按计划进行维护保养"
	}

	var rec string
	switch anomalyType {
	case "bearing_fault":
		rec = "检测到轴承故障特征：振动增大且呈上升趋势。"
		if remainingHours < 168 {
			rec += fmt.Sprintf("预计剩余寿命约%.0f小时，建议立即安排更换轴承。", remainingHours)
		} else {
			rec += fmt.Sprintf("预计剩余寿命约%.0f小时，建议1周内安排轴承检查更换。", remainingHours)
		}
	case "piston_ring_wear":
		rec = "检测到活塞环磨损特征：电流升高伴随振动增大。"
		if remainingHours < 72 {
			rec += "建议立即停机检修，更换活塞环组件。"
		} else {
			rec += fmt.Sprintf("预计剩余寿命约%.0f小时，建议3天内安排检修。", remainingHours)
		}
	case "imbalance":
		rec = "检测到转子不平衡特征：振动稳定偏高但无明显上升趋势。"
		rec += "建议在下次停机时做动平衡校正，暂时可继续运行但需加强监测。"
	case "motor_fault":
		rec = "检测到电机故障特征：电流波动大且标准差超限。"
		rec += "建议立即检查电机绝缘、轴承和供电系统，必要时切换至备用压缩机。"
	default:
		rec = fmt.Sprintf("检测到未知异常，异常评分%.2f。", anomalyScore)
		rec += "建议安排工程师进行现场检查，结合历史数据分析故障原因。"
	}

	return rec
}

func (s *BOGDiagnosticService) ensureModelTrained(ctx context.Context, tankID int) {
	if s.iforest.trees[0] != nil {
		return
	}

	history, err := s.db.GetBOGHistory(ctx, tankID, s.modelParams.HistoryWindowHours)
	if err != nil || len(history) < 10 {
		s.trainWithDefaultData()
		return
	}

	s.ensureModelTrainedWithData(ctx, tankID, history)
}

func (s *BOGDiagnosticService) ensureModelTrainedWithData(
	ctx context.Context,
	tankID int,
	history []models.BOGCompressorData,
) {
	if s.iforest.trees[0] != nil && len(history) < s.modelParams.IForestSampleSize {
		return
	}

	if len(history) < 10 {
		s.trainWithDefaultData()
		return
	}

	featureMatrix := make([][]float64, 0, len(history))
	for _, d := range history {
		if d.RunningStatus == 1 {
			feat := s.extractFeatures(d, history)
			featureMatrix = append(featureMatrix, feat)
		}
	}

	if len(featureMatrix) >= s.modelParams.IForestSampleSize/2 {
		s.iforest.Fit(featureMatrix)
	} else {
		s.trainWithDefaultData()
	}
}

func (s *BOGDiagnosticService) trainWithDefaultData() {
	nSamples := 500
	nFeatures := 8
	X := make([][]float64, nSamples)
	for i := 0; i < nSamples; i++ {
		X[i] = make([]float64, nFeatures)
		X[i][0] = s.modelParams.NormalVibrationRange[0] + rand.Float64()*(s.modelParams.NormalVibrationRange[1]-s.modelParams.NormalVibrationRange[0])
		X[i][1] = s.modelParams.NormalCurrentRange[0] + rand.Float64()*(s.modelParams.NormalCurrentRange[1]-s.modelParams.NormalCurrentRange[0])
		X[i][2] = 0.15 + rand.Float64()*0.1
		for j := 3; j < nFeatures; j++ {
			X[i][j] = 0.3 + rand.Float64()*0.4
		}
	}
	s.iforest.Fit(X)
}

func (s *BOGDiagnosticService) saveDiagnostic(ctx context.Context, result messages.BOGDiagnosticResult) {
	diag := &models.BOGDiagnostic{
		Time:           result.DiagnosedAt,
		TankID:         result.TankID,
		CompressorID:   result.CompressorID,
		AnomalyScore:   result.AnomalyScore,
		IsAnomaly:      result.IsAnomaly,
		AnomalyType:    result.AnomalyType,
		Confidence:     result.Confidence,
		RemainingHours: result.RemainingHours,
		Recommendation: result.Recommendation,
		ModelVersion:   s.cfg.BOGDiagnostic.ModelVersion,
	}

	if err := s.db.InsertBOGDiagnostic(ctx, diag); err != nil {
		fmt.Printf("Error saving BOG diagnostic: %v\n", err)
	}
}

func (s *BOGDiagnosticService) RunManualDiagnostic(
	ctx context.Context,
	tankID, compressorID, historyHours int,
) (*messages.BOGDiagnosticResult, error) {
	history, err := s.db.GetBOGHistory(ctx, tankID, historyHours)
	if err != nil {
		return nil, fmt.Errorf("get BOG history: %w", err)
	}
	if len(history) == 0 {
		return nil, fmt.Errorf("no BOG data found for tank %d compressor %d", tankID, compressorID)
	}

	var targetData models.BOGCompressorData
	found := false
	for _, d := range history {
		if d.CompressorID == compressorID {
			targetData = d
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("no data found for compressor %d", compressorID)
	}

	s.ensureModelTrainedWithData(ctx, tankID, history)
	result := s.diagnoseCompressor(ctx, tankID, targetData, history)
	go s.saveDiagnostic(ctx, result)

	return &result, nil
}
