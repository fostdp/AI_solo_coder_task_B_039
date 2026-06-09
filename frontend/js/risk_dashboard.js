class RiskDashboard {
    constructor() {
        this.currentTankId = 1;
        this.tanks = [];
        this.tankData = null;
        this.autoRefresh = true;
        this.refreshInterval = null;
        this.viewer = null;
        this.tempChart = null;
        this.densityChart = null;
        this.riskChart = null;
    }

    init(viewer) {
        this.viewer = viewer;
        this.viewer.onSensorClick = (sensorData) => this.handleSensorClick(sensorData);

        this.loadTanks();
        this.setupEventListeners();
        this.startAutoRefresh();
        this.updateTime();
        setInterval(() => this.updateTime(), 1000);

        this.refreshAllData();
    }

    async loadTanks() {
        try {
            this.tanks = await API.getTanks();
            this.renderTankButtons();
        } catch (e) {
            console.error('Failed to load tanks:', e);
            this.tanks = [
                { tank_id: 1, tank_name: 'T-101' },
                { tank_id: 2, tank_name: 'T-102' },
                { tank_id: 3, tank_name: 'T-103' },
                { tank_id: 4, tank_name: 'T-104' }
            ];
            this.renderTankButtons();
        }
    }

    renderTankButtons() {
        const container = document.getElementById('tank-buttons');
        container.innerHTML = '';

        this.tanks.forEach(tank => {
            const btn = document.createElement('div');
            btn.className = `tank-btn ${tank.tank_id === this.currentTankId ? 'active' : ''}`;
            btn.innerHTML = `
                <div class="tank-name">${tank.tank_name}</div>
                <div class="tank-risk risk-low" id="tank-risk-${tank.tank_id}">安全</div>
            `;
            btn.onclick = () => this.selectTank(tank.tank_id);
            container.appendChild(btn);
        });
    }

    selectTank(tankId) {
        this.currentTankId = tankId;
        this.viewer.setCurrentTank(tankId);
        document.querySelectorAll('.tank-btn').forEach(btn => btn.classList.remove('active'));
        document.querySelector(`.tank-btn:nth-child(${tankId})`)?.classList.add('active');
        this.refreshTankData();
    }

    setupEventListeners() {
        document.querySelectorAll('.view-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                document.querySelectorAll('.view-btn').forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                const view = btn.dataset.view;
                this.viewer.setView(view);
                this.refreshTankData();
            });
        });

        document.getElementById('refresh-btn').addEventListener('click', () => {
            this.refreshAllData();
        });

        document.getElementById('auto-refresh-toggle').addEventListener('click', () => {
            this.autoRefresh = !this.autoRefresh;
            const btn = document.getElementById('auto-refresh-toggle');
            const dot = btn.querySelector('.auto-dot');
            btn.dataset.active = this.autoRefresh;
            dot.classList.toggle('active', this.autoRefresh);

            if (this.autoRefresh) {
                this.startAutoRefresh();
            } else {
                this.stopAutoRefresh();
            }
        });

        window.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') {
                this.closeModal();
            }
        });
    }

    startAutoRefresh() {
        if (this.refreshInterval) return;
        this.refreshInterval = setInterval(() => {
            this.refreshAllData();
        }, CONFIG.REFRESH_INTERVAL);
    }

    stopAutoRefresh() {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
            this.refreshInterval = null;
        }
    }

    async refreshAllData() {
        await Promise.all([
            this.refreshTankData(),
            this.refreshAlarms(),
            this.refreshSystemStatus()
        ]);
    }

    async refreshTankData() {
        try {
            this.tankData = await API.getTank3DData(this.currentTankId);
            this.updateTankVisualization();
            this.updateTankInfo();
            this.updatePredictionPanel();
            this.updateTankRiskIndicator();
        } catch (e) {
            console.error('Failed to refresh tank data:', e);
            this.useMockData();
        }
    }

    useMockData() {
        this.tankData = {
            tank_id: this.currentTankId,
            tank_name: `T-10${this.currentTankId}`,
            layerTemps: [-161.5, -159.8, -158.2, -156.5, -155.0],
            densities: [425.2, 423.8, 422.5],
            densityHeights: [4, 24, 44],
            pressure: 0.18,
            risk_index: 0.15,
            alarms: []
        };

        this.updateTankVisualization();
        this.updateTankInfo();
        this.updatePredictionPanelMock();
        this.updateTankRiskIndicator();
    }

    updateTankVisualization() {
        if (!this.tankData) return;

        this.viewer.updateTemperatureData(this.tankData.layerTemps || []);
        this.viewer.updateDensityData(this.tankData.densities || []);
        this.viewer.drawOverlay(this.tankData);
    }

    updateTankInfo() {
        const overlay = document.getElementById('tank-info-overlay');
        if (!this.tankData) {
            overlay.innerHTML = '';
            return;
        }

        const tank = this.tanks.find(t => t.tank_id === this.currentTankId);
        const tankName = tank ? tank.tank_name : this.tankData.tank_name;

        const avgTemp = this.tankData.layerTemps
            ? (this.tankData.layerTemps.reduce((a, b) => a + b, 0) / this.tankData.layerTemps.length).toFixed(2)
            : '--';

        const maxTempDiff = this.tankData.layerTemps
            ? (Math.max(...this.tankData.layerTemps) - Math.min(...this.tankData.layerTemps)).toFixed(2)
            : '--';

        overlay.innerHTML = `
            <h4>${tankName} - 实时数据</h4>
            <div class="info-row">
                <span class="info-label">罐顶压力</span>
                <span class="info-value">${this.tankData.pressure?.toFixed(4) || '--'} MPa</span>
            </div>
            <div class="info-row">
                <span class="info-label">平均温度</span>
                <span class="info-value">${avgTemp} ℃</span>
            </div>
            <div class="info-row">
                <span class="info-label">最大温差</span>
                <span class="info-value">${maxTempDiff} ℃</span>
            </div>
            <div class="info-row">
                <span class="info-label">风险指数</span>
                <span class="info-value">${(this.tankData.risk_index * 100).toFixed(1)}%</span>
            </div>
        `;
    }

    async refreshAlarms() {
        try {
            const alarms = await API.getActiveAlarms();
            this.renderAlarms(alarms);
        } catch (e) {
            console.error('Failed to load alarms:', e);
            this.renderAlarms([]);
        }
    }

    renderAlarms(alarms) {
        const container = document.getElementById('alarm-list');

        if (alarms.length === 0) {
            container.innerHTML = '<div style="color: #666; text-align: center; padding: 20px;">暂无活动告警</div>';
            return;
        }

        container.innerHTML = alarms.map(alarm => `
            <div class="alarm-item level-${alarm.alarm_level}">
                <div class="alarm-type">${this.getAlarmTypeName(alarm.alarm_type)} ${alarm.alarm_level === 2 ? '⚠️' : '⚡'}</div>
                <div class="alarm-msg">${alarm.alarm_message}</div>
                <div class="alarm-time">${Visualization.formatTime(alarm.time)}</div>
            </div>
        `).join('');
    }

    getAlarmTypeName(type) {
        const names = {
            'ROLLOVER_WARNING': '翻滚预警',
            'OVERPRESSURE_ALARM': '超压告警'
        };
        return names[type] || type;
    }

    updatePredictionPanel() {
        if (!this.tankData) return;

        const riskIndex = this.tankData.risk_index || 0;
        Visualization.updateRiskGauge(riskIndex);

        API.getPrediction(this.currentTankId).then(prediction => {
            if (prediction) {
                document.getElementById('max-temp-diff').textContent = prediction.max_temp_diff?.toFixed(2) + ' ℃' || '--';
                document.getElementById('max-density-diff').textContent = prediction.max_density_diff?.toFixed(2) + ' kg/m³' || '--';
                document.getElementById('critical-time').textContent = prediction.critical_time_hours?.toFixed(1) + ' h' || '--';
                document.getElementById('stability').textContent = (prediction.stratification_stability * 100).toFixed(1) + '%' || '--';
                document.getElementById('recommendation').textContent = prediction.recommendation || '--';
            }
        }).catch(() => {
            this.updatePredictionPanelMock();
        });
    }

    updatePredictionPanelMock() {
        const riskIndex = this.tankData?.risk_index || 0.15;
        Visualization.updateRiskGauge(riskIndex);

        document.getElementById('max-temp-diff').textContent = '3.20 ℃';
        document.getElementById('max-density-diff').textContent = '1.80 kg/m³';
        document.getElementById('critical-time').textContent = '48.5 h';
        document.getElementById('stability').textContent = '75.2%';
        document.getElementById('recommendation').textContent = '安全，分层稳定';
    }

    updateTankRiskIndicator() {
        if (!this.tankData) return;

        this.tanks.forEach(tank => {
            const riskEl = document.getElementById(`tank-risk-${tank.tank_id}`);
            if (tank.tank_id === this.currentTankId) {
                const riskClass = Visualization.getRiskClass(this.tankData.risk_index);
                const riskText = Visualization.getRiskText(this.tankData.risk_index);
                riskEl.className = `tank-risk ${riskClass}`;
                riskEl.textContent = riskText;
            }
        });
    }

    async refreshSystemStatus() {
        try {
            const health = await API.healthCheck();
            const statusEl = document.getElementById('system-status');
            const dot = statusEl.querySelector('.status-dot');

            if (health.status === 'healthy') {
                dot.style.background = '#00ff00';
                statusEl.lastChild.textContent = ' 系统正常';
            } else {
                dot.style.background = '#ff0000';
                statusEl.lastChild.textContent = ' 系统异常';
            }
        } catch (e) {
            console.error('Health check failed:', e);
        }
    }

    updateTime() {
        const now = new Date();
        document.getElementById('current-time').textContent = now.toLocaleString('zh-CN', {
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
        });
    }

    async handleSensorClick(sensorData) {
        if (sensorData.type === 'sensor') {
            await this.showTemperatureSensorModal(sensorData);
        } else if (sensorData.type === 'density_sensor') {
            await this.showDensitySensorModal(sensorData);
        }
    }

    async showTemperatureSensorModal(sensorData) {
        const modal = document.getElementById('sensor-modal');
        document.getElementById('modal-title').textContent =
            `温度传感器 - 第${sensorData.layer}层 #${sensorData.sensor_index + 1}`;

        document.getElementById('sensor-info').innerHTML = `
            <div class="sensor-info-card">
                <div class="label">当前温度</div>
                <div class="value">${sensorData.temperature.toFixed(2)} ℃</div>
            </div>
            <div class="sensor-info-card">
                <div class="label">所在层级</div>
                <div class="value">第 ${sensorData.layer} 层</div>
            </div>
            <div class="sensor-info-card">
                <div class="label">传感器编号</div>
                <div class="value">#${sensorData.sensor_index + 1}</div>
            </div>
            <div class="sensor-info-card">
                <div class="label">安装高度</div>
                <div class="value">${CONFIG.LAYER_HEIGHTS[sensorData.layer - 1]} m</div>
            </div>
        `;

        try {
            const tempData = await API.getSensorTrend(
                this.currentTankId,
                sensorData.layer,
                sensorData.sensor_index,
                24
            );
            this.renderTemperatureChart(tempData);
        } catch (e) {
            console.error('Failed to load trend data:', e);
            this.renderMockTemperatureChart();
        }

        try {
            const prediction = await API.getPrediction(this.currentTankId);
            this.renderRiskChart(prediction);
        } catch (e) {
            this.renderMockRiskChart();
        }

        modal.style.display = 'block';
    }

    async showDensitySensorModal(sensorData) {
        const modal = document.getElementById('sensor-modal');
        document.getElementById('modal-title').textContent =
            `密度传感器 - #${sensorData.sensor_index + 1}`;

        document.getElementById('sensor-info').innerHTML = `
            <div class="sensor-info-card">
                <div class="label">当前密度</div>
                <div class="value">${sensorData.density.toFixed(2)} kg/m³</div>
            </div>
            <div class="sensor-info-card">
                <div class="label">传感器编号</div>
                <div class="value">#${sensorData.sensor_index + 1}</div>
            </div>
            <div class="sensor-info-card">
                <div class="label">安装高度</div>
                <div class="value">${CONFIG.DENSITY_HEIGHTS[sensorData.sensor_index]} m</div>
            </div>
            <div class="sensor-info-card">
                <div class="label">传感器类型</div>
                <div class="value">密度计</div>
            </div>
        `;

        try {
            const densityData = await API.getDensityTrend(
                this.currentTankId,
                sensorData.sensor_index,
                24
            );
            this.renderDensityChart(densityData);
        } catch (e) {
            console.error('Failed to load density trend:', e);
            this.renderMockDensityChart();
        }

        try {
            const prediction = await API.getPrediction(this.currentTankId);
            this.renderRiskChart(prediction);
        } catch (e) {
            this.renderMockRiskChart();
        }

        modal.style.display = 'block';
    }

    renderTemperatureChart(data) {
        const chartDom = document.getElementById('temperature-chart');
        if (!this.tempChart) {
            this.tempChart = echarts.init(chartDom);
        }

        const times = data.map(d => Visualization.formatTime(d.time));
        const temps = data.map(d => d.temperature);

        const option = {
            backgroundColor: 'transparent',
            title: {
                text: '温度趋势 (近24小时)',
                textStyle: { color: '#00ffff', fontSize: 14 }
            },
            tooltip: {
                trigger: 'axis',
                backgroundColor: 'rgba(0, 0, 0, 0.8)',
                borderColor: '#00ffff',
                textStyle: { color: '#fff' }
            },
            grid: { left: '10%', right: '5%', top: '15%', bottom: '10%' },
            xAxis: {
                type: 'category',
                data: times,
                axisLine: { lineStyle: { color: '#444' } },
                axisLabel: { color: '#888', fontSize: 10 }
            },
            yAxis: {
                type: 'value',
                name: '温度 (℃)',
                nameTextStyle: { color: '#888' },
                axisLine: { lineStyle: { color: '#444' } },
                axisLabel: { color: '#888' },
                splitLine: { lineStyle: { color: '#222' } }
            },
            series: [{
                name: '温度',
                type: 'line',
                data: temps,
                smooth: true,
                lineStyle: { color: '#00ffff', width: 2 },
                areaStyle: {
                    color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
                        { offset: 0, color: 'rgba(0, 255, 255, 0.3)' },
                        { offset: 1, color: 'rgba(0, 255, 255, 0)' }
                    ])
                },
                itemStyle: { color: '#00ffff' }
            }]
        };

        this.tempChart.setOption(option);
    }

    renderDensityChart(data) {
        const chartDom = document.getElementById('density-chart');
        if (!this.densityChart) {
            this.densityChart = echarts.init(chartDom);
        }

        const times = data.map(d => Visualization.formatTime(d.time));
        const densities = data.map(d => d.density);

        const option = {
            backgroundColor: 'transparent',
            title: {
                text: '密度趋势 (近24小时)',
                textStyle: { color: '#ff8800', fontSize: 14 }
            },
            tooltip: {
                trigger: 'axis',
                backgroundColor: 'rgba(0, 0, 0, 0.8)',
                borderColor: '#ff8800',
                textStyle: { color: '#fff' }
            },
            grid: { left: '10%', right: '5%', top: '15%', bottom: '10%' },
            xAxis: {
                type: 'category',
                data: times,
                axisLine: { lineStyle: { color: '#444' } },
                axisLabel: { color: '#888', fontSize: 10 }
            },
            yAxis: {
                type: 'value',
                name: '密度 (kg/m³)',
                nameTextStyle: { color: '#888' },
                axisLine: { lineStyle: { color: '#444' } },
                axisLabel: { color: '#888' },
                splitLine: { lineStyle: { color: '#222' } }
            },
            series: [{
                name: '密度',
                type: 'line',
                data: densities,
                smooth: true,
                lineStyle: { color: '#ff8800', width: 2 },
                areaStyle: {
                    color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
                        { offset: 0, color: 'rgba(255, 136, 0, 0.3)' },
                        { offset: 1, color: 'rgba(255, 136, 0, 0)' }
                    ])
                },
                itemStyle: { color: '#ff8800' }
            }]
        };

        this.densityChart.setOption(option);
    }

    renderRiskChart(prediction) {
        const chartDom = document.getElementById('risk-chart');
        if (!this.riskChart) {
            this.riskChart = echarts.init(chartDom);
        }

        const times = [];
        const risks = [];
        const now = new Date();
        for (let i = 24; i >= 0; i--) {
            const time = new Date(now - i * 3600000);
            times.push(time.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }));
            const baseRisk = prediction?.risk_index || 0.15;
            risks.push(Math.max(0, Math.min(1, baseRisk + (Math.random() - 0.5) * 0.1)));
        }

        const option = {
            backgroundColor: 'transparent',
            tooltip: {
                trigger: 'axis',
                backgroundColor: 'rgba(0, 0, 0, 0.8)',
                textStyle: { color: '#fff' },
                formatter: (params) => {
                    const val = params[0].value;
                    const level = val >= 0.8 ? '高风险' : val >= 0.6 ? '中风险' : val >= 0.2 ? '低风险' : '安全';
                    return `${params[0].axisValue}<br/>风险指数: ${(val * 100).toFixed(1)}%<br/>等级: ${level}`;
                }
            },
            grid: { left: '10%', right: '5%', top: '10%', bottom: '10%' },
            xAxis: {
                type: 'category',
                data: times,
                axisLine: { lineStyle: { color: '#444' } },
                axisLabel: { color: '#888', fontSize: 10 }
            },
            yAxis: {
                type: 'value',
                name: '风险指数 (%)',
                nameTextStyle: { color: '#888' },
                axisLine: { lineStyle: { color: '#444' } },
                axisLabel: {
                    color: '#888',
                    formatter: (val) => (val * 100).toFixed(0) + '%'
                },
                splitLine: { lineStyle: { color: '#222' } },
                min: 0,
                max: 1
            },
            visualMap: {
                show: false,
                pieces: [
                    { gt: 0.8, color: '#ff0000' },
                    { gt: 0.6, lte: 0.8, color: '#ffff00' },
                    { gt: 0.2, lte: 0.6, color: '#00ff00' },
                    { lte: 0.2, color: '#00ffff' }
                ]
            },
            series: [{
                name: '风险指数',
                type: 'line',
                data: risks,
                smooth: true,
                lineStyle: { width: 2 },
                areaStyle: {
                    color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
                        { offset: 0, color: 'rgba(255, 0, 0, 0.3)' },
                        { offset: 1, color: 'rgba(255, 0, 0, 0)' }
                    ])
                },
                markLine: {
                    silent: true,
                    lineStyle: { type: 'dashed' },
                    data: [
                        { yAxis: 0.8, label: { position: 'end', formatter: '高风险', color: '#ff0000' } },
                        { yAxis: 0.6, label: { position: 'end', formatter: '中风险', color: '#ffff00' } },
                        { yAxis: 0.2, label: { position: 'end', formatter: '低风险', color: '#00ff00' } }
                    ]
                }
            }]
        };

        this.riskChart.setOption(option);
    }

    renderMockTemperatureChart() {
        const mockData = [];
        const now = new Date();
        for (let i = 24; i >= 0; i--) {
            mockData.push({
                time: new Date(now - i * 3600000),
                temperature: -160 + Math.random() * 5
            });
        }
        this.renderTemperatureChart(mockData);
    }

    renderMockDensityChart() {
        const mockData = [];
        const now = new Date();
        for (let i = 24; i >= 0; i--) {
            mockData.push({
                time: new Date(now - i * 3600000),
                density: 423 + Math.random() * 3
            });
        }
        this.renderDensityChart(mockData);
    }

    renderMockRiskChart() {
        this.renderRiskChart(null);
    }

    closeModal() {
        document.getElementById('sensor-modal').style.display = 'none';
    }

    resizeCharts() {
        if (this.tempChart) this.tempChart.resize();
        if (this.densityChart) this.densityChart.resize();
        if (this.riskChart) this.riskChart.resize();
    }
}

window.addEventListener('load', () => {
    const viewer = new Tank3DViewer('three-canvas', 'overlay-canvas');
    window.dashboard = new RiskDashboard();
    window.dashboard.init(viewer);
});

window.addEventListener('resize', () => {
    if (window.dashboard) {
        window.dashboard.resizeCharts();
    }
});
