const API = {
    async getTanks() {
        const response = await fetch(`${CONFIG.API_BASE_URL}/tanks`);
        return await response.json();
    },

    async getTank3DData(tankId) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/tanks/${tankId}/data`);
        return await response.json();
    },

    async getTemperatureData(tankId) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/tanks/${tankId}/temperature`);
        return await response.json();
    },

    async getDensityData(tankId) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/tanks/${tankId}/density`);
        return await response.json();
    },

    async getPressureData(tankId) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/tanks/${tankId}/pressure`);
        return await response.json();
    },

    async getPrediction(tankId) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/tanks/${tankId}/prediction`);
        return await response.json();
    },

    async getLayerSummary(tankId, duration = 24) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/tanks/${tankId}/layer-summary?duration=${duration}`);
        return await response.json();
    },

    async getSensorTrend(tankId, layer, sensor, duration = 24) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/sensors/${tankId}/${layer}/${sensor}/trend?duration=${duration}`);
        return await response.json();
    },

    async getDensityTrend(tankId, sensor, duration = 24) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/density-sensors/${tankId}/${sensor}/trend?duration=${duration}`);
        return await response.json();
    },

    async getActiveAlarms() {
        const response = await fetch(`${CONFIG.API_BASE_URL}/alarms`);
        return await response.json();
    },

    async acknowledgeAlarm(alarmId) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/alarms/${alarmId}/acknowledge`, {
            method: 'POST'
        });
        return await response.json();
    },

    async clearAlarm(alarmId) {
        const response = await fetch(`${CONFIG.API_BASE_URL}/alarms/${alarmId}/clear`, {
            method: 'POST'
        });
        return await response.json();
    },

    async getAlarmConfig() {
        const response = await fetch(`${CONFIG.API_BASE_URL}/alarm-config`);
        return await response.json();
    },

    async healthCheck() {
        try {
            const response = await fetch(`${CONFIG.API_BASE_URL}/health`);
            return await response.json();
        } catch (e) {
            return { status: 'unhealthy' };
        }
    }
};
