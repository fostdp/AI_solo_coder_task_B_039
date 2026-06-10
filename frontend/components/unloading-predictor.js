const UnloadingPredictor = {
    currentTankId: 1,
    charts: {},

    init(tankId) {
        if (tankId !== undefined) {
            this.currentTankId = tankId;
        }
        this.bindEvents();
        this.initCharts();
    },

    setCurrentTank(tankId) {
        this.currentTankId = tankId;
    },

    bindEvents() {
        document.getElementById('show-unloading-form').addEventListener('click', () => this.toggleUnloadingForm());
        document.getElementById('run-unloading-prediction').addEventListener('click', () => this.runUnloadingPrediction());
    },

    initCharts() {
        this.charts.unloadingTemp = echarts.init(document.getElementById('unloading-temp-chart'));
        this.charts.unloadingDensity = echarts.init(document.getElementById('unloading-density-chart'));

        window.addEventListener('resize', () => {
            Object.values(this.charts).forEach(chart => {
                if (chart && chart.resize) chart.resize();
            });
        });
    },

    refresh() {
        this.refreshUnloadingPrediction();
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
    }
};
