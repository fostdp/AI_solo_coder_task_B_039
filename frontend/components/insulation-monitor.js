const InsulationMonitor = {
    currentTankId: 1,
    charts: {},

    init(tankId = 1) {
        this.currentTankId = tankId;
        this.bindEvents();
        this.initCharts();
    },

    setCurrentTank(tankId) {
        this.currentTankId = tankId;
    },

    bindEvents() {
        document.getElementById('run-heatleak-eval').addEventListener('click', () => this.runHeatLeakEvaluation());
    },

    initCharts() {
        this.charts.heatleakTrend = echarts.init(document.getElementById('heatleak-trend-chart'));

        window.addEventListener('resize', () => {
            if (this.charts.heatleakTrend && this.charts.heatleakTrend.resize) {
                this.charts.heatleakTrend.resize();
            }
        });
    },

    refresh() {
        this.refreshHeatLeakAssessment();
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
    }
};
