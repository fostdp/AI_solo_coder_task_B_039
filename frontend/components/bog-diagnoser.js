const BOGDiagnoser = {
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
        document.getElementById('run-bog-diagnostic').addEventListener('click', () => this.runBOGDiagnostic());
        document.getElementById('compressor-select').addEventListener('change', () => this.refreshBOGDiagnostic());
    },

    initCharts() {
        this.charts.bogTrend = echarts.init(document.getElementById('bog-trend-chart'));

        window.addEventListener('resize', () => {
            if (this.charts.bogTrend && this.charts.bogTrend.resize) {
                this.charts.bogTrend.resize();
            }
        });
    },

    refresh() {
        this.refreshBOGDiagnostic();
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
    }
};
