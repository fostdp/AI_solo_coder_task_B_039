let currentView = '3d';
let currentTankId = 1;
let autoRefreshActive = true;
let autoRefreshInterval = null;

document.addEventListener('DOMContentLoaded', function() {
    initFeatureDashboards();
    bindViewButtons();
    bindTankButtons();
    bindControlButtons();
    startAutoRefresh();
    updateCurrentTime();
    setInterval(updateCurrentTime, 1000);
});

function initFeatureDashboards() {
    if (typeof FeatureDashboards !== 'undefined') {
        FeatureDashboards.init();
        FeatureDashboards.setCurrentTank(currentTankId);
    }
    if (typeof MultiTankScheduler !== 'undefined') {
        MultiTankScheduler.init(currentTankId);
    }
}

function bindViewButtons() {
    const viewButtons = document.querySelectorAll('.view-btn');
    viewButtons.forEach(btn => {
        btn.addEventListener('click', function() {
            const view = this.getAttribute('data-view');
            switchView(view);
        });
    });
}

function bindTankButtons() {
    const tankButtonsContainer = document.getElementById('tank-buttons');
    if (tankButtonsContainer) {
        tankButtonsContainer.addEventListener('click', function(e) {
            if (e.target.classList.contains('tank-btn')) {
                const tankId = parseInt(e.target.getAttribute('data-tank-id'));
                selectTank(tankId);
            }
        });
    }
}

function bindControlButtons() {
    const refreshBtn = document.getElementById('refresh-btn');
    if (refreshBtn) {
        refreshBtn.addEventListener('click', refreshAllData);
    }

    const autoRefreshToggle = document.getElementById('auto-refresh-toggle');
    if (autoRefreshToggle) {
        autoRefreshToggle.addEventListener('click', toggleAutoRefresh);
    }
}

function switchView(viewName) {
    currentView = viewName;

    const viewButtons = document.querySelectorAll('.view-btn');
    viewButtons.forEach(btn => {
        btn.classList.remove('active');
        if (btn.getAttribute('data-view') === viewName) {
            btn.classList.add('active');
        }
    });

    if (typeof FeatureDashboards !== 'undefined') {
        FeatureDashboards.switchView(viewName);
    }

    if (typeof Tank3DViewer !== 'undefined') {
        if (viewName === '3d') {
            Tank3DViewer.show3DView();
        } else if (viewName === 'section') {
            Tank3DViewer.showSectionView();
        } else if (viewName === 'heatmap') {
            Tank3DViewer.showHeatmapView();
        }
    }
}

function selectTank(tankId) {
    currentTankId = tankId;

    const tankButtons = document.querySelectorAll('.tank-btn');
    tankButtons.forEach(btn => {
        btn.classList.remove('active');
        if (parseInt(btn.getAttribute('data-tank-id')) === tankId) {
            btn.classList.add('active');
        }
    });

    if (typeof FeatureDashboards !== 'undefined') {
        FeatureDashboards.setCurrentTank(tankId);
        FeatureDashboards.refreshAll();
    }
    if (typeof MultiTankScheduler !== 'undefined') {
        MultiTankScheduler.setCurrentTank(tankId);
    }

    if (typeof Tank3DViewer !== 'undefined') {
        Tank3DViewer.switchTank(tankId);
    }

    if (typeof RiskDashboard !== 'undefined') {
        RiskDashboard.loadTankData(tankId);
    }
}

function refreshAllData() {
    if (typeof FeatureDashboards !== 'undefined') {
        FeatureDashboards.refreshAll();
    }

    if (typeof Tank3DViewer !== 'undefined') {
        Tank3DViewer.refreshData();
    }

    if (typeof RiskDashboard !== 'undefined') {
        RiskDashboard.loadTankData(currentTankId);
    }

    if (typeof MultiTankScheduler !== 'undefined') {
        MultiTankScheduler.refresh();
    }

    loadActiveAlarms();
    checkSystemHealth();
}

function startAutoRefresh() {
    if (autoRefreshInterval) {
        clearInterval(autoRefreshInterval);
    }

    autoRefreshInterval = setInterval(() => {
        if (autoRefreshActive) {
            refreshAllData();
        }
    }, 5000);
}

function toggleAutoRefresh() {
    autoRefreshActive = !autoRefreshActive;
    const toggle = document.getElementById('auto-refresh-toggle');
    const dot = toggle.querySelector('.auto-dot');

    if (autoRefreshActive) {
        toggle.setAttribute('data-active', 'true');
        dot.classList.add('active');
    } else {
        toggle.setAttribute('data-active', 'false');
        dot.classList.remove('active');
    }
}

function updateCurrentTime() {
    const timeElement = document.getElementById('current-time');
    if (timeElement) {
        const now = new Date();
        timeElement.textContent = now.toLocaleString('zh-CN', {
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
        });
    }
}

async function loadActiveAlarms() {
    try {
        const alarms = await API.getActiveAlarms();
        const alarmList = document.getElementById('alarm-list');
        if (alarmList) {
            if (alarms && alarms.length > 0) {
                alarmList.innerHTML = alarms.map(alarm => `
                    <div class="alarm-item ${alarm.alarm_level || 'info'}">
                        <div class="alarm-header">
                            <span class="alarm-title">${alarm.alarm_name || '告警'}</span>
                            <span class="alarm-time">${new Date(alarm.time).toLocaleTimeString('zh-CN')}</span>
                        </div>
                        <div class="alarm-message">${alarm.message || ''}</div>
                        ${alarm.tank_id ? `<div class="alarm-tank">${alarm.tank_id}#储罐</div>` : ''}
                    </div>
                `).join('');
            } else {
                alarmList.innerHTML = '<div class="no-alarms">暂无活动告警</div>';
            }
        }
    } catch (e) {
        console.error('Failed to load alarms:', e);
    }
}

async function checkSystemHealth() {
    try {
        const health = await API.healthCheck();
        const statusElement = document.getElementById('system-status');
        const dotElement = statusElement.querySelector('.status-dot');

        if (health.status === 'healthy') {
            statusElement.innerHTML = '<span class="status-dot"></span>系统正常';
        } else {
            statusElement.innerHTML = '<span class="status-dot error"></span>系统异常';
        }
    } catch (e) {
        const statusElement = document.getElementById('system-status');
        statusElement.innerHTML = '<span class="status-dot error"></span>连接中断';
    }
}
