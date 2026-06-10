const FeatureDashboards = {
    currentTankId: 1,
    components: {},

    init() {
        this.initComponents();
        this.bindEvents();
    },

    initComponents() {
        if (typeof BOGDiagnoser !== 'undefined') {
            BOGDiagnoser.init(this.currentTankId);
            this.components.bogDiagnoser = BOGDiagnoser;
        }
        if (typeof InsulationMonitor !== 'undefined') {
            InsulationMonitor.init(this.currentTankId);
            this.components.insulationMonitor = InsulationMonitor;
        }
        if (typeof UnloadingPredictor !== 'undefined') {
            UnloadingPredictor.init(this.currentTankId);
            this.components.unloadingPredictor = UnloadingPredictor;
        }
        if (typeof MultiTankScheduler !== 'undefined') {
            MultiTankScheduler.init(this.currentTankId);
            this.components.multiTankScheduler = MultiTankScheduler;
        }
    },

    setCurrentTank(tankId) {
        this.currentTankId = tankId;
        if (this.components.bogDiagnoser) {
            this.components.bogDiagnoser.setCurrentTank(tankId);
        }
        if (this.components.insulationMonitor) {
            this.components.insulationMonitor.setCurrentTank(tankId);
        }
        if (this.components.unloadingPredictor) {
            this.components.unloadingPredictor.setCurrentTank(tankId);
        }
        if (this.components.multiTankScheduler) {
            this.components.multiTankScheduler.setCurrentTank(tankId);
        }
    },

    bindEvents() {
        if (this.components.bogDiagnoser) {
            this.components.bogDiagnoser.bindEvents();
        }
        if (this.components.insulationMonitor) {
            this.components.insulationMonitor.bindEvents();
        }
        if (this.components.unloadingPredictor) {
            this.components.unloadingPredictor.bindEvents();
        }
        if (this.components.multiTankScheduler) {
            this.components.multiTankScheduler.bindEvents();
        }
    },

    refreshAll() {
        if (this.components.bogDiagnoser) {
            this.components.bogDiagnoser.refresh();
        }
        if (this.components.insulationMonitor) {
            this.components.insulationMonitor.refresh();
        }
        if (this.components.unloadingPredictor) {
            this.components.unloadingPredictor.refresh();
        }
        if (this.components.multiTankScheduler) {
            this.components.multiTankScheduler.refresh();
        }
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

            if (viewName === 'bog' && this.components.bogDiagnoser) {
                this.components.bogDiagnoser.refresh();
            }
            if (viewName === 'heatleak' && this.components.insulationMonitor) {
                this.components.insulationMonitor.refresh();
            }
            if (viewName === 'unloading' && this.components.unloadingPredictor) {
                this.components.unloadingPredictor.refresh();
            }
            if (viewName === 'schedule' && this.components.multiTankScheduler) {
                this.components.multiTankScheduler.refresh();
            }

            setTimeout(() => {
                this.resizeAllCharts();
            }, 100);
        }
    },

    resizeAllCharts() {
        if (this.components.bogDiagnoser && this.components.bogDiagnoser.charts) {
            Object.values(this.components.bogDiagnoser.charts).forEach(chart => {
                if (chart && chart.resize) chart.resize();
            });
        }
        if (this.components.insulationMonitor && this.components.insulationMonitor.charts) {
            Object.values(this.components.insulationMonitor.charts).forEach(chart => {
                if (chart && chart.resize) chart.resize();
            });
        }
        if (this.components.unloadingPredictor && this.components.unloadingPredictor.charts) {
            Object.values(this.components.unloadingPredictor.charts).forEach(chart => {
                if (chart && chart.resize) chart.resize();
            });
        }
    }
};
