const CONFIG = {
    API_BASE_URL: 'http://localhost:8080/api',
    REFRESH_INTERVAL: 5000,
    TANK_HEIGHT: 48,
    TANK_DIAMETER: 82,
    LAYERS: 5,
    THERMOMETERS_PER_LAYER: 8,
    DENSITY_METERS: 3,
    LAYER_HEIGHTS: [4, 14, 24, 34, 44],
    DENSITY_HEIGHTS: [4, 24, 44],
    TEMP_MIN: -165,
    TEMP_MAX: -150,
    DENSITY_MIN: 420,
    DENSITY_MAX: 430,
    COLOR_SCALE: [
        { value: -165, color: '#0000ff' },
        { value: -161, color: '#0080ff' },
        { value: -158, color: '#00ffff' },
        { value: -155, color: '#00ff80' },
        { value: -153, color: '#ffff00' },
        { value: -151, color: '#ff8000' },
        { value: -150, color: '#ff0000' }
    ],
    CONTOUR_LEVELS: [422, 423, 424, 425, 426],
    RISK_THRESHOLDS: {
        LOW: 0.2,
        MEDIUM: 0.6,
        HIGH: 0.8
    }
};
