const MultiTankScheduler = {
    currentTankId: 1,

    init(tankId) {
        if (tankId !== undefined) {
            this.currentTankId = tankId;
        }
        this.bindEvents();
    },

    setCurrentTank(tankId) {
        this.currentTankId = tankId;
    },

    bindEvents() {
        document.getElementById('run-schedule-optimization').addEventListener('click', () => this.runScheduleOptimization());
        document.getElementById('show-cost-breakdown').addEventListener('click', () => this.toggleCostBreakdown());
    },

    refresh() {
        this.refreshSchedule();
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
    }
};
