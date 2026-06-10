const FeatureDashboards = {
    currentTankId: 1,
    charts: {},

    init() {
        this.bindEvents();
        this.initCharts();
    },

    setCurrentTank(tankId) {
        this.currentTankId = tankId;
    },

    bindEvents() {
        document.getElementById('run-bog-diagnostic').addEventListener('click', () => this.runBOGDiagnostic());
        document.getElementById('compressor-select').addEventListener('change', () => this.refreshBOGDiagnostic());
        document.getElementById('run-heatleak-eval').addEventListener('click', () => this.runHeatLeakEvaluation());
        document.getElementById('show-unloading-form').addEventListener('click', () => this.toggleUnloadingForm());
        document.getElementById('run-unloading-prediction').addEventListener('click', () => this.runUnloadingPrediction());
        document.getElementById('run-schedule-optimization').addEventListener('click', () => this.runScheduleOptimization());
        document.getElementById('show-cost-breakdown').addEventListener('click', () => this.toggleCostBreakdown());
    },

    initCharts() {
        this.charts.bogTrend = echarts.init(document.getElementById('bog-trend-chart'));
        this.charts.heatleakTrend = echarts.init(document.getElementById('heatleak-trend-chart'));
        this.charts.unloadingTemp = echarts.init(document.getElementById('unloading-temp-chart'));
        this.charts.unloadingDensity = echarts.init(document.getElementById('unloading-density-chart'));

        window.addEventListener('resize', () => {
            Object.values(this.charts).forEach(chart => {
                if (chart && chart.resize) chart.resize();
            });
        });
    },

    refreshAll() {
        this.refreshBOGDiagnostic();
        this.refreshHeatLeakAssessment();
        this.refreshUnloadingPrediction();
        this.refreshSchedule();
    },

    async refreshBOGDiagnostic() {
        try {
            const compressorId = parseInt(document.getElementById('compressor-select').value);
            const data = await API.getBOGDiagnostic(this.currentTankId, compressorId);
            this.updateBOGDiagnostic(data);

            const history = await API.getBOGDiagnosticHistory(this.currentTankId, 72);
            this.updateBOGTrendChart(history);
        } catch (e) {
            console.error('Failed to refresh BOG diagnostic:', e);
        }
    },

    async runBOGDiagnostic() {
        try {
            const compressorId = parseInt(document.getElementById('compressor-select').value);
            const result = await API.runBOGDiagnostic(this.currentTankId, compressorId, 24);
            this.updateBOGDiagnostic(result);
            this.refreshBOGDiagnostic();
        } catch (e) {
            console.error('Failed to run BOG diagnostic:', e);
            alert('诊断失败: ' + e.message);
        }
    },

    updateBOGDiagnostic(data) {
        if (!data || Object.keys(data).length === 0) {
            document.getElementById('bog-status-title').textContent = '暂无数据';
            document.getElementById('bog-status-subtitle').textContent = '异常评分: --';
            return;
        }

        const statusCard = document.getElementById('bog-status-card');
        const statusIcon = document.getElementById('bog-status-icon');
        const statusTitle = document.getElementById('bog-status-title');
        const statusSubtitle = document.getElementById('bog-status-subtitle');

        if (data.is_anomaly) {
            statusCard.className = 'status-card danger';
            statusIcon.textContent = '⚠';
            statusTitle.textContent = '检测到异常';
        } else {
            statusCard.className = 'status-card success';
            statusIcon.textContent = '✓';
            statusTitle.textContent = '运行正常';
        }

        statusSubtitle.textContent = `异常评分: ${(data.anomaly_score * 100).toFixed(1)}%`;

        document.getElementById('bog-anomaly-type').textContent = data.anomaly_type || '无';
        document.getElementById('bog-confidence').textContent = data.confidence ? (data.confidence * 100).toFixed(1) + '%' : '--';
        document.getElementById('bog-remaining').textContent = data.remaining_hours ? data.remaining_hours.toFixed(1) + ' 小时' : '--';
        document.getElementById('bog-time').textContent = data.diagnosed_at ? new Date(data.diagnosed_at).toLocaleString('zh-CN') : '--';
        document.getElementById('bog-recommendation-text').textContent = data.recommendation || '暂无建议';
    },

    updateBOGTrendChart(history) {
        if (!history || history.length === 0) return;

        const times = history.map(h => new Date(h.time).toLocaleString('zh-CN'));
        const scores = history.map(h => h.anomaly_score * 100);

        const option = {
            tooltip: { trigger: 'axis' },
            grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true },
            xAxis: { type: 'category', data: times, axisLabel: { rotate: 45 } },
            yAxis: { type: 'value', min: 0, max: 100, axisLabel: { formatter: '{value}%' } },
            series: [{
                name: '异常评分',
                type: 'line',
                data: scores,
                smooth: true,
                areaStyle: { opacity: 0.3 },
                lineStyle: { color: '#ff6b6b' },
                itemStyle: { color: '#ff6b6b' },
                markLine: {
                    data: [{ yAxis: 70, label: { formatter: '阈值 70%' }, lineStyle: { color: '#ff0000', type: 'dashed' } }]
                }
            }]
        };

        this.charts.bogTrend.setOption(option);
    },

    async refreshHeatLeakAssessment() {
        try {
            const data = await API.getHeatLeakAssessment(this.currentTankId);
            this.updateHeatLeakAssessment(data);

            const history = await API.getHeatLeakHistory(this.currentTankId, 168);
            this.updateHeatLeakTrendChart(history);
        } catch (e) {
            console.error('Failed to refresh heat leak assessment:', e);
        }
    },

    async runHeatLeakEvaluation() {
        try {
            const ambientTemp = parseFloat(document.getElementById('ambient-temp-input').value);
            const result = await API.runHeatLeakEvaluation(this.currentTankId, ambientTemp, 24);
            this.updateHeatLeakAssessment(result);
            this.refreshHeatLeakAssessment();
        } catch (e) {
            console.error('Failed to run heat leak evaluation:', e);
            alert('评估失败: ' + e.message);
        }
    },

    updateHeatLeakAssessment(data) {
        if (!data || Object.keys(data).length === 0) {
            document.getElementById('heatleak-status-title').textContent = '暂无数据';
            document.getElementById('heatleak-status-subtitle').textContent = '保冷性能: --';
            return;
        }

        const statusCard = document.getElementById('heatleak-status-card');
        const statusIcon = document.getElementById('heatleak-status-icon');
        const statusTitle = document.getElementById('heatleak-status-title');
        const statusSubtitle = document.getElementById('heatleak-status-subtitle');

        const performance = data.insulation_performance * 100;

        if (data.is_warning) {
            statusCard.className = 'status-card warning';
            statusIcon.textContent = '⚠';
            statusTitle.textContent = '保冷性能下降';
        } else if (performance < 90) {
            statusCard.className = 'status-card warning';
            statusIcon.textContent = '⚠';
            statusTitle.textContent = '保冷性能预警';
        } else {
            statusCard.className = 'status-card success';
            statusIcon.textContent = '✓';
            statusTitle.textContent = '保冷良好';
        }

        statusSubtitle.textContent = `保冷性能: ${performance.toFixed(1)}%`;

        document.getElementById('heatleak-conductivity').textContent = data.equivalent_conductivity ? data.equivalent_conductivity.toFixed(4) + ' W/m·K' : '--';
        document.getElementById('heatleak-rate').textContent = data.heat_leak_rate ? data.heat_leak_rate.toFixed(2) + ' W/m²' : '--';
        document.getElementById('heatleak-load').textContent = data.total_heat_load_kw ? data.total_heat_load_kw.toFixed(2) + ' kW' : '--';
        document.getElementById('heatleak-time').textContent = data.time ? new Date(data.time).toLocaleString('zh-CN') : '--';

        const leakRegions = data.leak_regions || [];
        if (leakRegions.length > 0) {
            document.getElementById('leak-regions-list').innerHTML = leakRegions.map(r => `<span class="leak-tag">第${r}层</span>`).join('');
        } else {
            document.getElementById('leak-regions-list').textContent = '未检测到漏热区域';
        }
    },

    updateHeatLeakTrendChart(history) {
        if (!history || history.length === 0) return;

        const times = history.map(h => new Date(h.time).toLocaleString('zh-CN'));
        const performances = history.map(h => h.insulation_performance * 100);

        const option = {
            tooltip: { trigger: 'axis' },
            grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true },
            xAxis: { type: 'category', data: times, axisLabel: { rotate: 45 } },
            yAxis: { type: 'value', min: 50, max: 120, axisLabel: { formatter: '{value}%' } },
            series: [{
                name: '保冷性能',
                type: 'line',
                data: performances,
                smooth: true,
                areaStyle: { opacity: 0.3, color: '#4ecdc4' },
                lineStyle: { color: '#4ecdc4' },
                itemStyle: { color: '#4ecdc4' },
                markLine: {
                    data: [{ yAxis: 80, label: { formatter: '预警阈值 80%' }, lineStyle: { color: '#ffa500', type: 'dashed' } }]
                }
            }]
        };

        this.charts.heatleakTrend.setOption(option);
    },

    toggleUnloadingForm() {
        const form = document.getElementById('unloading-form');
        form.classList.toggle('hidden');
    },

    async refreshUnloadingPrediction() {
        try {
            const data = await API.getUnloadingPrediction(this.currentTankId);
            this.updateUnloadingPrediction(data);
        } catch (e) {
            console.error('Failed to refresh unloading prediction:', e);
        }
    },

    async runUnloadingPrediction() {
        try {
            const request = {
                tank_id: this.currentTankId,
                unloading_rate: parseFloat(document.getElementById('unloading-rate').value),
                unloading_density: parseFloat(document.getElementById('unloading-density').value),
                unloading_temp: parseFloat(document.getElementById('unloading-temp').value),
                estimated_duration: parseFloat(document.getElementById('unloading-duration').value)
            };

            const result = await API.runUnloadingPrediction(request);
            this.updateUnloadingPrediction(result);
            document.getElementById('unloading-form').classList.add('hidden');
        } catch (e) {
            console.error('Failed to run unloading prediction:', e);
            alert('预测失败: ' + e.message);
        }
    },

    updateUnloadingPrediction(data) {
        if (!data || Object.keys(data).length === 0) {
            document.getElementById('unloading-max-temp').textContent = '--';
            document.getElementById('unloading-max-density').textContent = '--';
            document.getElementById('unloading-pump-time').textContent = '--';
            document.getElementById('unloading-risk').textContent = '--';
            return;
        }

        document.getElementById('unloading-max-temp').textContent = data.max_temp_diff ? data.max_temp_diff.toFixed(2) + ' ℃' : '--';
        document.getElementById('unloading-max-density').textContent = data.max_density_diff ? data.max_density_diff.toFixed(2) + ' kg/m³' : '--';
        document.getElementById('unloading-pump-time').textContent = data.optimal_pump_on_time !== undefined ? data.optimal_pump_on_time.toFixed(1) + ' 小时后' : '--';
        document.getElementById('unloading-risk').textContent = data.rollover_risk ? (data.rollover_risk * 100).toFixed(1) + '%' : '--';

        if (data.predicted_temps && data.time_steps) {
            this.updateUnloadingTempChart(data);
        }
        if (data.predicted_densities && data.time_steps) {
            this.updateUnloadingDensityChart(data);
        }
    },

    updateUnloadingTempChart(data) {
        const timeSteps = data.time_steps;
        const temps = data.predicted_temps;

        const series = [];
        const nLayers = temps[0].length;

        for (let i = 0; i < nLayers; i += Math.max(1, Math.floor(nLayers / 5))) {
            series.push({
                name: `第${i + 1}层`,
                type: 'line',
                data: temps.map(t => t[i]),
                smooth: true
            });
        }

        const option = {
            tooltip: { trigger: 'axis' },
            legend: { data: series.map(s => s.name) },
            grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true },
            xAxis: { type: 'category', data: timeSteps.map(t => t.toFixed(1) + 'h'), name: '时间 (小时)' },
            yAxis: { type: 'value', name: '温度 (℃)' },
            series: series
        };

        this.charts.unloadingTemp.setOption(option);
    },

    updateUnloadingDensityChart(data) {
        const timeSteps = data.time_steps;
        const densities = data.predicted_densities;

        const series = [];
        const nLayers = densities[0].length;

        for (let i = 0; i < nLayers; i += Math.max(1, Math.floor(nLayers / 5))) {
            series.push({
                name: `第${i + 1}层`,
                type: 'line',
                data: densities.map(d => d[i]),
                smooth: true
            });
        }

        const option = {
            tooltip: { trigger: 'axis' },
            legend: { data: series.map(s => s.name) },
            grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true },
            xAxis: { type: 'category', data: timeSteps.map(t => t.toFixed(1) + 'h'), name: '时间 (小时)' },
            yAxis: { type: 'value', name: '密度 (kg/m³)' },
            series: series
        };

        this.charts.unloadingDensity.setOption(option);
    },

    async refreshSchedule() {
        try {
            const data = await API.getLatestSchedule();
            this.updateSchedule(data);
        } catch (e) {
            console.error('Failed to refresh schedule:', e);
        }
    },

    async runScheduleOptimization() {
        try {
            const result = await API.runScheduleOptimization();
            this.updateSchedule(result);
            this.refreshSchedule();
        } catch (e) {
            console.error('Failed to run schedule optimization:', e);
            alert('优化失败: ' + e.message);
        }
    },

    async toggleCostBreakdown() {
        const breakdown = document.getElementById('cost-breakdown');
        if (breakdown.classList.contains('hidden')) {
            try {
                const data = await API.getScheduleCostBreakdown();
                this.updateCostBreakdown(data);
            } catch (e) {
                console.error('Failed to get cost breakdown:', e);
            }
        }
        breakdown.classList.toggle('hidden');
    },

    updateSchedule(data) {
        if (!data || Object.keys(data).length === 0) {
            document.getElementById('schedule-evaporation').textContent = '--';
            document.getElementById('schedule-time').textContent = '--';
            return;
        }

        document.getElementById('schedule-evaporation').textContent = data.evaporation_loss ? data.evaporation_loss.toFixed(4) : '--';
        document.getElementById('schedule-time').textContent = data.optimized_at ? new Date(data.optimized_at).toLocaleString('zh-CN') : '--';

        this.updateCompressorLoads(data.compressor_loads);
        this.updatePumpOperations(data.pump_operations);
    },

    updateCompressorLoads(loads) {
        const container = document.getElementById('compressor-loads-grid');
        if (!loads) {
            container.innerHTML = '<p>暂无数据</p>';
            return;
        }

        container.innerHTML = Object.entries(loads).map(([key, value]) => {
            const load = typeof value === 'number' ? value : 0;
            const color = load > 80 ? '#ff6b6b' : load > 50 ? '#ffa500' : '#4ecdc4';
            return `
                <div class="compressor-item">
                    <div class="compressor-name">${key}</div>
                    <div class="compressor-bar">
                        <div class="compressor-fill" style="width: ${load}%; background: ${color};"></div>
                    </div>
                    <div class="compressor-value">${load.toFixed(0)}%</div>
                </div>
            `;
        }).join('');
    },

    updatePumpOperations(operations) {
        const container = document.getElementById('pump-operations-list');
        if (!operations || operations.length === 0) {
            container.innerHTML = '<p>暂无泵运行计划</p>';
            return;
        }

        container.innerHTML = operations.map(op => `
            <div class="pump-item">
                <div class="pump-info">
                    <span class="pump-name">${op.tank_id}#罐 ${op.pump_id}#泵</span>
                    <span class="pump-action ${op.action}">${op.action === 'start' ? '启动' : op.action === 'stop' ? '停止' : '运行'}</span>
                </div>
                <div class="pump-timing">
                    <span>${op.start_time.toFixed(1)}小时后启动</span>
                    <span>运行${op.duration.toFixed(1)}小时</span>
                </div>
            </div>
        `).join('');
    },

    updateCostBreakdown(data) {
        if (!data) return;

        const formatCurrency = (val) => '¥' + (val || 0).toFixed(2) + '/小时';

        document.getElementById('cost-evaporation').textContent = formatCurrency(data.evaporation_loss_cost);
        document.getElementById('cost-compressor').textContent = formatCurrency(data.compressor_power_cost);
        document.getElementById('cost-pump').textContent = formatCurrency(data.pump_power_cost);
        document.getElementById('cost-total').textContent = formatCurrency(data.total_operating_cost);
    },

    switchView(viewName) {
        const views = ['3d', 'section', 'heatmap', 'bog', 'heatleak', 'unloading', 'schedule'];
        const canvasContainer = document.getElementById('canvas-container');

        views.forEach(view => {
            const panel = document.getElementById(view + '-panel');
            if (panel) {
                panel.classList.add('hidden');
            }
        });

        if (['3d', 'section', 'heatmap'].includes(viewName)) {
            canvasContainer.classList.remove('hidden');
        } else {
            canvasContainer.classList.add('hidden');
            const panel = document.getElementById(viewName + '-panel');
            if (panel) {
                panel.classList.remove('hidden');
            }

            if (viewName === 'bog') this.refreshBOGDiagnostic();
            if (viewName === 'heatleak') this.refreshHeatLeakAssessment();
            if (viewName === 'unloading') this.refreshUnloadingPrediction();
            if (viewName === 'schedule') this.refreshSchedule();

            setTimeout(() => {
                Object.values(this.charts).forEach(chart => {
                    if (chart && chart.resize) chart.resize();
                });
            }, 100);
        }
    }
};
