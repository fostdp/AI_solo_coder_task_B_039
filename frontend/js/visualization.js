const Visualization = {
    getTemperatureColor(temp) {
        const clampedTemp = Math.max(CONFIG.TEMP_MIN, Math.min(CONFIG.TEMP_MAX, temp));
        for (let i = 0; i < CONFIG.COLOR_SCALE.length - 1; i++) {
            const curr = CONFIG.COLOR_SCALE[i];
            const next = CONFIG.COLOR_SCALE[i + 1];
            if (clampedTemp >= curr.value && clampedTemp <= next.value) {
                const t = (clampedTemp - curr.value) / (next.value - curr.value);
                return this.interpolateColor(curr.color, next.color, t);
            }
        }
        return CONFIG.COLOR_SCALE[CONFIG.COLOR_SCALE.length - 1].color;
    },

    interpolateColor(color1, color2, t) {
        const c1 = this.hexToRgb(color1);
        const c2 = this.hexToRgb(color2);
        const r = Math.round(c1.r + (c2.r - c1.r) * t);
        const g = Math.round(c1.g + (c2.g - c1.g) * t);
        const b = Math.round(c1.b + (c2.b - c1.b) * t);
        return `rgb(${r}, ${g}, ${b})`;
    },

    hexToRgb(hex) {
        const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
        return result ? {
            r: parseInt(result[1], 16),
            g: parseInt(result[2], 16),
            b: parseInt(result[3], 16)
        } : null;
    },

    getRiskClass(riskIndex) {
        if (riskIndex >= CONFIG.RISK_THRESHOLDS.HIGH) return 'risk-high';
        if (riskIndex >= CONFIG.RISK_THRESHOLDS.MEDIUM) return 'risk-medium';
        return 'risk-low';
    },

    getRiskText(riskIndex) {
        if (riskIndex >= CONFIG.RISK_THRESHOLDS.HIGH) return '高风险';
        if (riskIndex >= CONFIG.RISK_THRESHOLDS.MEDIUM) return '中风险';
        if (riskIndex >= CONFIG.RISK_THRESHOLDS.LOW) return '低风险';
        return '安全';
    },

    drawContourLines(ctx, width, height, densityData, densityHeights) {
        const radius = width * 0.35;
        const centerX = width / 2;
        const centerY = height / 2;

        const heights = CONFIG.LAYER_HEIGHTS;
        const grid = this.createDensityGrid(densityData, densityHeights, heights);

        for (let level of CONFIG.CONTOUR_LEVELS) {
            const contourPoints = this.marchingSquares(grid, level);
            if (contourPoints.length < 2) continue;

            const levelIndex = CONFIG.CONTOUR_LEVELS.indexOf(level);
            const opacity = 0.4 + (levelIndex / CONFIG.CONTOUR_LEVELS.length) * 0.6;
            const lineWidth = 1 + (levelIndex / CONFIG.CONTOUR_LEVELS.length) * 2;

            ctx.strokeStyle = `rgba(255, 136, 0, ${opacity})`;
            ctx.lineWidth = lineWidth;
            ctx.setLineDash([]);
            ctx.beginPath();

            for (let i = 0; i < contourPoints.length; i++) {
                const [gx, gy] = contourPoints[i];
                const angle = (gx / grid[0].length) * Math.PI * 2;
                const h = 1 - (gy / grid.length);
                const r = radius * h;
                const x = centerX + Math.cos(angle) * r;
                const y = centerY - Math.sin(angle) * r * 0.8;

                if (i === 0) {
                    ctx.moveTo(x, y);
                } else {
                    ctx.lineTo(x, y);
                }
            }
            ctx.stroke();
        }
    },

    createDensityGrid(densityData, densityHeights, layerHeights) {
        const gridSize = 50;
        const grid = [];

        for (let y = 0; y < gridSize; y++) {
            grid[y] = [];
            const h = (y / (gridSize - 1)) * CONFIG.TANK_HEIGHT;
            for (let x = 0; x < gridSize; x++) {
                grid[y][x] = this.interpolateDensity(densityData, densityHeights, h);
            }
        }
        return grid;
    },

    interpolateDensity(densities, heights, targetHeight) {
        if (targetHeight <= heights[0]) return densities[0];
        if (targetHeight >= heights[heights.length - 1]) return densities[densities.length - 1];

        for (let i = 0; i < heights.length - 1; i++) {
            if (targetHeight >= heights[i] && targetHeight <= heights[i + 1]) {
                const t = (targetHeight - heights[i]) / (heights[i + 1] - heights[i]);
                return densities[i] + t * (densities[i + 1] - densities[i]);
            }
        }
        return densities[densities.length - 1];
    },

    marchingSquares(grid, level) {
        const points = [];
        for (let y = 0; y < grid.length - 1; y++) {
            for (let x = 0; x < grid[0].length - 1; x++) {
                const a = grid[y][x];
                const b = grid[y][x + 1];
                const c = grid[y + 1][x + 1];
                const d = grid[y + 1][x];

                let idx = 0;
                if (a > level) idx |= 1;
                if (b > level) idx |= 2;
                if (c > level) idx |= 4;
                if (d > level) idx |= 8;

                if (idx === 0 || idx === 15) continue;

                const t1 = (level - a) / (b - a);
                const t2 = (level - b) / (c - b);
                const t3 = (level - d) / (c - d);
                const t4 = (level - a) / (d - a);

                const edgePoints = {
                    1: [[x + t1, y]],
                    2: [[x + 1, y + t2]],
                    3: [[x + t1, y], [x + 1, y + t2]],
                    4: [[x + 1 - t2, y + 1]],
                    5: [[x + t1, y], [x + 1 - t2, y + 1]],
                    6: [[x + 1, y + t2], [x + 1 - t2, y + 1]],
                    7: [[x + t1, y], [x + 1, y + t2], [x + 1 - t2, y + 1]],
                    8: [[x, y + t4]],
                    9: [[x + t1, y], [x, y + t4]],
                    10: [[x + 1, y + t2], [x, y + t4]],
                    11: [[x + t1, y], [x + 1, y + t2], [x, y + t4]],
                    12: [[x + 1 - t2, y + 1], [x, y + t4]],
                    13: [[x + t1, y], [x + 1 - t2, y + 1], [x, y + t4]],
                    14: [[x + 1, y + t2], [x + 1 - t2, y + 1], [x, y + t4]]
                };

                if (edgePoints[idx]) {
                    points.push(...edgePoints[idx]);
                }
            }
        }
        return points;
    },

    drawSectionView(ctx, width, height, layerTemps, densities, densityHeights) {
        ctx.clearRect(0, 0, width, height);

        const sectionWidth = width * 0.6;
        const sectionHeight = height * 0.85;
        const startX = (width - sectionWidth) / 2;
        const startY = (height - sectionHeight) / 2;

        const layerCount = CONFIG.LAYERS;
        const layerHeight = sectionHeight / layerCount;

        for (let i = 0; i < layerCount; i++) {
            const temp = layerTemps[i] || CONFIG.TEMP_MIN;
            const color = this.getTemperatureColor(temp);
            const y = startY + i * layerHeight;

            const gradient = ctx.createLinearGradient(startX, y, startX + sectionWidth, y + layerHeight);
            gradient.addColorStop(0, this.adjustBrightness(color, -20));
            gradient.addColorStop(0.5, color);
            gradient.addColorStop(1, this.adjustBrightness(color, -20));

            ctx.fillStyle = gradient;
            ctx.fillRect(startX, y, sectionWidth, layerHeight);

            ctx.strokeStyle = 'rgba(255, 255, 255, 0.2)';
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(startX, y);
            ctx.lineTo(startX + sectionWidth, y);
            ctx.stroke();

            ctx.fillStyle = 'rgba(255, 255, 255, 0.9)';
            ctx.font = '11px Arial';
            ctx.textAlign = 'left';
            ctx.fillText(`L${i + 1}: ${temp.toFixed(2)}℃`, startX + 10, y + layerHeight / 2 + 4);
            ctx.fillText(`H: ${CONFIG.LAYER_HEIGHTS[i]}m`, startX + 10, y + layerHeight / 2 + 18);
        }

        ctx.strokeStyle = 'rgba(0, 255, 255, 0.5)';
        ctx.lineWidth = 2;
        ctx.strokeRect(startX, startY, sectionWidth, sectionHeight);

        this.drawDensityProfile(ctx, startX + sectionWidth + 30, startY, 40, sectionHeight, densities, densityHeights);
        this.drawTemperatureScale(ctx, 20, startY, 30, sectionHeight);
    },

    adjustBrightness(color, amount) {
        const rgb = color.match(/\d+/g);
        if (!rgb) return color;
        const r = Math.max(0, Math.min(255, parseInt(rgb[0]) + amount));
        const g = Math.max(0, Math.min(255, parseInt(rgb[1]) + amount));
        const b = Math.max(0, Math.min(255, parseInt(rgb[2]) + amount));
        return `rgb(${r}, ${g}, ${b})`;
    },

    drawDensityProfile(ctx, startX, startY, width, height, densities, heights) {
        ctx.fillStyle = 'rgba(0, 0, 0, 0.3)';
        ctx.fillRect(startX - 10, startY - 20, width + 20, height + 40);

        ctx.fillStyle = '#00ffff';
        ctx.font = 'bold 11px Arial';
        ctx.textAlign = 'center';
        ctx.fillText('密度', startX + width / 2, startY - 5);

        const maxDensity = Math.max(...densities) + 2;
        const minDensity = Math.min(...densities) - 2;

        ctx.strokeStyle = 'rgba(255, 255, 255, 0.2)';
        ctx.lineWidth = 1;
        for (let i = 0; i <= 4; i++) {
            const y = startY + (i / 4) * height;
            ctx.beginPath();
            ctx.moveTo(startX, y);
            ctx.lineTo(startX + width, y);
            ctx.stroke();

            const val = maxDensity - (i / 4) * (maxDensity - minDensity);
            ctx.fillStyle = 'rgba(255, 255, 255, 0.6)';
            ctx.font = '9px Arial';
            ctx.textAlign = 'left';
            ctx.fillText(val.toFixed(0), startX + width + 5, y + 3);
        }

        ctx.strokeStyle = '#ff8800';
        ctx.lineWidth = 2;
        ctx.beginPath();
        for (let i = 0; i < densities.length; i++) {
            const hRatio = 1 - (heights[i] / CONFIG.TANK_HEIGHT);
            const y = startY + hRatio * height;
            const valRatio = (densities[i] - minDensity) / (maxDensity - minDensity);
            const x = startX + (1 - valRatio) * width;

            if (i === 0) {
                ctx.moveTo(x, y);
            } else {
                ctx.lineTo(x, y);
            }
        }
        ctx.stroke();

        for (let i = 0; i < densities.length; i++) {
            const hRatio = 1 - (heights[i] / CONFIG.TANK_HEIGHT);
            const y = startY + hRatio * height;
            const valRatio = (densities[i] - minDensity) / (maxDensity - minDensity);
            const x = startX + (1 - valRatio) * width;

            ctx.fillStyle = '#ff8800';
            ctx.beginPath();
            ctx.arc(x, y, 4, 0, Math.PI * 2);
            ctx.fill();

            ctx.fillStyle = '#fff';
            ctx.font = '10px Arial';
            ctx.textAlign = 'center';
            ctx.fillText(`${densities[i].toFixed(1)}`, x, y - 8);
        }
    },

    drawTemperatureScale(ctx, startX, startY, width, height) {
        const gradient = ctx.createLinearGradient(0, startY, 0, startY + height);
        for (let i = 0; i < CONFIG.COLOR_SCALE.length; i++) {
            const stop = CONFIG.COLOR_SCALE[i];
            const ratio = (stop.value - CONFIG.TEMP_MIN) / (CONFIG.TEMP_MAX - CONFIG.TEMP_MIN);
            gradient.addColorStop(ratio, stop.color);
        }

        ctx.fillStyle = gradient;
        ctx.fillRect(startX, startY, width, height);

        ctx.strokeStyle = 'rgba(255, 255, 255, 0.5)';
        ctx.lineWidth = 1;
        ctx.strokeRect(startX, startY, width, height);

        ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
        ctx.font = '10px Arial';
        ctx.textAlign = 'left';
        for (let i = 0; i <= 5; i++) {
            const y = startY + (i / 5) * height;
            const temp = CONFIG.TEMP_MAX - (i / 5) * (CONFIG.TEMP_MAX - CONFIG.TEMP_MIN);
            ctx.fillText(temp.toFixed(0) + '℃', startX + width + 8, y + 4);
        }
    },

    updateRiskGauge(riskIndex) {
        const arc = document.getElementById('risk-arc');
        const value = document.getElementById('risk-value');
        const pointer = document.getElementById('risk-pointer');

        const maxDash = 158;
        const dashOffset = maxDash * (1 - riskIndex);
        arc.style.strokeDasharray = `${maxDash - dashOffset} ${maxDash}`;

        value.textContent = (riskIndex * 100).toFixed(1) + '%';

        const angle = Math.PI * riskIndex;
        const pointerX = 60 + 45 * Math.cos(angle);
        const pointerY = 55 - 45 * Math.sin(angle);
        pointer.setAttribute('cx', pointerX);
        pointer.setAttribute('cy', pointerY);

        let color;
        if (riskIndex >= CONFIG.RISK_THRESHOLDS.HIGH) {
            color = '#ff0000';
        } else if (riskIndex >= CONFIG.RISK_THRESHOLDS.MEDIUM) {
            color = '#ffff00';
        } else {
            color = '#00ff00';
        }
        value.style.color = color;
    },

    formatTime(dateStr) {
        const date = new Date(dateStr);
        return date.toLocaleString('zh-CN', {
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit'
        });
    },

    formatDateTime(dateStr) {
        const date = new Date(dateStr);
        return date.toLocaleString('zh-CN');
    }
};
