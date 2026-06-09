class TankScene {
    constructor(canvasId) {
        this.canvas = document.getElementById(canvasId);
        this.overlayCanvas = document.getElementById('overlay-canvas');
        this.ctx = this.overlayCanvas.getContext('2d');
        this.currentView = '3d';
        this.currentTankId = 1;
        this.sensors = [];
        this.raycaster = new THREE.Raycaster();
        this.mouse = new THREE.Vector2();
        this.selectedSensor = null;
        this.onSensorClick = null;

        this.init();
        this.setupEventListeners();
        this.animate();
    }

    init() {
        const container = this.canvas.parentElement;
        this.width = container.clientWidth;
        this.height = container.clientHeight;

        this.scene = new THREE.Scene();
        this.scene.background = new THREE.Color(0x0a0a15);
        this.scene.fog = new THREE.Fog(0x0a0a15, 50, 200);

        this.camera = new THREE.PerspectiveCamera(45, this.width / this.height, 0.1, 1000);
        this.camera.position.set(80, 40, 80);

        this.renderer = new THREE.WebGLRenderer({
            canvas: this.canvas,
            antialias: true,
            alpha: true
        });
        this.renderer.setSize(this.width, this.height);
        this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
        this.renderer.shadowMap.enabled = true;
        this.renderer.shadowMap.type = THREE.PCFSoftShadowMap;

        this.controls = new THREE.OrbitControls(this.camera, this.canvas);
        this.controls.enableDamping = true;
        this.controls.dampingFactor = 0.05;
        this.controls.minDistance = 30;
        this.controls.maxDistance = 200;
        this.controls.maxPolarAngle = Math.PI / 2 + 0.1;

        this.setupLighting();
        this.createTank();
        this.resizeOverlay();
    }

    setupLighting() {
        const ambientLight = new THREE.AmbientLight(0x404040, 0.5);
        this.scene.add(ambientLight);

        const directionalLight = new THREE.DirectionalLight(0xffffff, 0.8);
        directionalLight.position.set(50, 100, 50);
        directionalLight.castShadow = true;
        directionalLight.shadow.mapSize.width = 2048;
        directionalLight.shadow.mapSize.height = 2048;
        directionalLight.shadow.camera.near = 0.5;
        directionalLight.shadow.camera.far = 500;
        directionalLight.shadow.camera.left = -100;
        directionalLight.shadow.camera.right = 100;
        directionalLight.shadow.camera.top = 100;
        directionalLight.shadow.camera.bottom = -100;
        this.scene.add(directionalLight);

        const pointLight1 = new THREE.PointLight(0x00ffff, 0.3, 100);
        pointLight1.position.set(-50, 30, -50);
        this.scene.add(pointLight1);

        const pointLight2 = new THREE.PointLight(0x0080ff, 0.2, 100);
        pointLight2.position.set(50, 60, -50);
        this.scene.add(pointLight2);
    }

    createTank() {
        this.tankGroup = new THREE.Group();
        this.tankGroup.userData.type = 'tank';

        const tankHeight = CONFIG.TANK_HEIGHT;
        const tankRadius = CONFIG.TANK_DIAMETER / 2;

        const outerGeometry = new THREE.CylinderGeometry(tankRadius + 0.5, tankRadius + 0.5, tankHeight, 64, 1, true);
        const outerMaterial = new THREE.MeshPhongMaterial({
            color: 0x1a1a2e,
            transparent: true,
            opacity: 0.3,
            side: THREE.DoubleSide,
            wireframe: false
        });
        const outerShell = new THREE.Mesh(outerGeometry, outerMaterial);
        outerShell.position.y = tankHeight / 2;
        outerShell.receiveShadow = true;
        this.tankGroup.add(outerShell);

        const wireframeGeometry = new THREE.CylinderGeometry(tankRadius + 0.5, tankRadius + 0.5, tankHeight, 32, 8, true);
        const wireframeMaterial = new THREE.MeshBasicMaterial({
            color: 0x00ffff,
            transparent: true,
            opacity: 0.15,
            wireframe: true
        });
        const wireframe = new THREE.Mesh(wireframeGeometry, wireframeMaterial);
        wireframe.position.y = tankHeight / 2;
        this.tankGroup.add(wireframe);

        this.layerMeshes = [];
        const layerHeight = tankHeight / CONFIG.LAYERS;
        const layerRadius = tankRadius - 1;

        for (let i = 0; i < CONFIG.LAYERS; i++) {
            const layerGeometry = new THREE.CylinderGeometry(layerRadius, layerRadius, layerHeight - 0.2, 32);
            const layerMaterial = new THREE.MeshPhongMaterial({
                color: 0x0000ff,
                transparent: true,
                opacity: 0.6,
                side: THREE.DoubleSide
            });
            const layerMesh = new THREE.Mesh(layerGeometry, layerMaterial);
            layerMesh.position.y = i * layerHeight + layerHeight / 2;
            layerMesh.userData.layer = i + 1;
            layerMesh.userData.type = 'layer';
            this.layerMeshes.push(layerMesh);
            this.tankGroup.add(layerMesh);

            const ringGeometry = new THREE.TorusGeometry(layerRadius + 0.1, 0.05, 8, 32);
            const ringMaterial = new THREE.MeshBasicMaterial({ color: 0x00ffff, transparent: true, opacity: 0.5 });
            const ring = new THREE.Mesh(ringGeometry, ringMaterial);
            ring.position.y = (i + 1) * layerHeight;
            ring.rotation.x = Math.PI / 2;
            this.tankGroup.add(ring);
        }

        const baseGeometry = new THREE.CylinderGeometry(tankRadius + 1, tankRadius + 2, 2, 32);
        const baseMaterial = new THREE.MeshPhongMaterial({ color: 0x2a2a4e });
        const base = new THREE.Mesh(baseGeometry, baseMaterial);
        base.position.y = -1;
        base.receiveShadow = true;
        this.tankGroup.add(base);

        const topGeometry = new THREE.CylinderGeometry(tankRadius + 0.5, tankRadius + 1, 1, 32);
        const topMaterial = new THREE.MeshPhongMaterial({ color: 0x2a2a4e });
        const top = new THREE.Mesh(topGeometry, topMaterial);
        top.position.y = tankHeight + 0.5;
        top.receiveShadow = true;
        this.tankGroup.add(top);

        const domeGeometry = new THREE.SphereGeometry(tankRadius + 0.5, 32, 16, 0, Math.PI * 2, 0, Math.PI / 2);
        const domeMaterial = new THREE.MeshPhongMaterial({
            color: 0x1a1a2e,
            transparent: true,
            opacity: 0.4
        });
        const dome = new THREE.Mesh(domeGeometry, domeMaterial);
        dome.position.y = tankHeight + 1;
        this.tankGroup.add(dome);

        this.createSensors(tankRadius, tankHeight);
        this.scene.add(this.tankGroup);
    }

    createSensors(tankRadius, tankHeight) {
        this.sensorGroup = new THREE.Group();
        this.sensors = [];

        const sensorRadius = tankRadius - 2;

        for (let layer = 0; layer < CONFIG.LAYERS; layer++) {
            const y = CONFIG.LAYER_HEIGHTS[layer];
            for (let i = 0; i < CONFIG.THERMOMETERS_PER_LAYER; i++) {
                const angle = (i / CONFIG.THERMOMETERS_PER_LAYER) * Math.PI * 2;
                const x = Math.cos(angle) * sensorRadius;
                const z = Math.sin(angle) * sensorRadius;

                const geometry = new THREE.SphereGeometry(0.4, 16, 16);
                const material = new THREE.MeshBasicMaterial({
                    color: 0x00ffff,
                    transparent: true,
                    opacity: 0.8
                });
                const sensor = new THREE.Mesh(geometry, material);
                sensor.position.set(x, y, z);
                sensor.userData = {
                    type: 'sensor',
                    layer: layer + 1,
                    sensorIndex: i,
                    temperature: CONFIG.TEMP_MIN
                };

                const glowGeometry = new THREE.SphereGeometry(0.6, 16, 16);
                const glowMaterial = new THREE.MeshBasicMaterial({
                    color: 0x00ffff,
                    transparent: true,
                    opacity: 0.2
                });
                const glow = new THREE.Mesh(glowGeometry, glowMaterial);
                sensor.add(glow);

                this.sensors.push(sensor);
                this.sensorGroup.add(sensor);
            }
        }

        for (let i = 0; i < CONFIG.DENSITY_METERS; i++) {
            const y = CONFIG.DENSITY_HEIGHTS[i];
            const geometry = new THREE.CylinderGeometry(0.3, 0.3, 1.5, 16);
            const material = new THREE.MeshBasicMaterial({
                color: 0xff8800,
                transparent: true,
                opacity: 0.9
            });
            const sensor = new THREE.Mesh(geometry, material);
            sensor.position.set(0, y, sensorRadius - 1);
            sensor.userData = {
                type: 'density_sensor',
                sensorIndex: i,
                density: 420
            };

            const glowGeometry = new THREE.CylinderGeometry(0.5, 0.5, 1.7, 16);
            const glowMaterial = new THREE.MeshBasicMaterial({
                color: 0xff8800,
                transparent: true,
                opacity: 0.2
            });
            const glow = new THREE.Mesh(glowGeometry, glowMaterial);
            sensor.add(glow);

            this.sensors.push(sensor);
            this.sensorGroup.add(sensor);
        }

        this.tankGroup.add(this.sensorGroup);
    }

    updateTemperatureData(layerTemps) {
        const layerHeight = CONFIG.TANK_HEIGHT / CONFIG.LAYERS;

        for (let i = 0; i < this.layerMeshes.length; i++) {
            const temp = layerTemps[i] || CONFIG.TEMP_MIN;
            const colorStr = Visualization.getTemperatureColor(temp);
            const color = new THREE.Color(colorStr);
            this.layerMeshes[i].material.color = color;
            this.layerMeshes[i].material.opacity = 0.5 + ((temp - CONFIG.TEMP_MIN) / (CONFIG.TEMP_MAX - CONFIG.TEMP_MIN)) * 0.3;
        }

        this.sensors.forEach(sensor => {
            if (sensor.userData.type === 'sensor') {
                const layer = sensor.userData.layer - 1;
                const temp = layerTemps[layer] || CONFIG.TEMP_MIN;
                sensor.userData.temperature = temp;
                const colorStr = Visualization.getTemperatureColor(temp);
                const color = new THREE.Color(colorStr);
                sensor.material.color = color;
                if (sensor.children[0]) {
                    sensor.children[0].material.color = color;
                }
            }
        });
    }

    updateDensityData(densities) {
        this.sensors.forEach(sensor => {
            if (sensor.userData.type === 'density_sensor') {
                const idx = sensor.userData.sensorIndex;
                if (densities[idx]) {
                    sensor.userData.density = densities[idx];
                }
            }
        });
    }

    setView(view) {
        this.currentView = view;

        if (view === 'section') {
            this.camera.position.set(0, CONFIG.TANK_HEIGHT / 2, 100);
            this.camera.lookAt(0, CONFIG.TANK_HEIGHT / 2, 0);
            this.controls.enabled = false;
            this.tankGroup.rotation.y = 0;
        } else if (view === 'heatmap') {
            this.camera.position.set(0, 120, 0.1);
            this.camera.lookAt(0, 0, 0);
            this.controls.enabled = false;
        } else {
            this.camera.position.set(80, 40, 80);
            this.camera.lookAt(0, CONFIG.TANK_HEIGHT / 2, 0);
            this.controls.enabled = true;
        }
    }

    drawOverlay(data) {
        this.ctx.clearRect(0, 0, this.width, this.height);

        if (this.currentView === 'section' && data) {
            Visualization.drawSectionView(
                this.ctx,
                this.width,
                this.height,
                data.layerTemps || [],
                data.densities || [],
                data.densityHeights || CONFIG.DENSITY_HEIGHTS
            );
        } else if (this.currentView === 'heatmap' && data) {
            this.drawHeatmap(data);
        }
    }

    drawHeatmap(data) {
        const ctx = this.ctx;
        const centerX = this.width / 2;
        const centerY = this.height / 2;
        const radius = Math.min(this.width, this.height) * 0.35;

        const layerTemps = data.layerTemps || [];

        for (let i = 0; i < layerTemps.length; i++) {
            const r1 = radius * (i / layerTemps.length);
            const r2 = radius * ((i + 1) / layerTemps.length);
            const temp = layerTemps[i];
            const color = Visualization.getTemperatureColor(temp);

            ctx.fillStyle = color;
            ctx.globalAlpha = 0.7;
            ctx.beginPath();
            ctx.arc(centerX, centerY, r2, 0, Math.PI * 2);
            ctx.arc(centerX, centerY, r1, 0, Math.PI * 2, true);
            ctx.fill();
            ctx.globalAlpha = 1;

            ctx.fillStyle = 'rgba(255, 255, 255, 0.9)';
            ctx.font = 'bold 14px Arial';
            ctx.textAlign = 'center';
            const midR = (r1 + r2) / 2;
            ctx.fillText(`L${i + 1}: ${temp.toFixed(1)}℃`, centerX + midR * 0.7, centerY - midR * 0.3);
        }

        if (data.densities) {
            Visualization.drawContourLines(ctx, this.width, this.height, data.densities, data.densityHeights || CONFIG.DENSITY_HEIGHTS);
        }

        for (let i = 0; i < 8; i++) {
            const angle = (i / 8) * Math.PI * 2;
            const x = centerX + Math.cos(angle) * (radius + 20);
            const y = centerY + Math.sin(angle) * (radius + 20);
            ctx.fillStyle = '#00ffff';
            ctx.beginPath();
            ctx.arc(x, y, 4, 0, Math.PI * 2);
            ctx.fill();
        }
    }

    setupEventListeners() {
        window.addEventListener('resize', () => this.onResize());

        this.canvas.addEventListener('click', (e) => this.onClick(e));
        this.canvas.addEventListener('mousemove', (e) => this.onMouseMove(e));
    }

    onResize() {
        const container = this.canvas.parentElement;
        this.width = container.clientWidth;
        this.height = container.clientHeight;

        this.camera.aspect = this.width / this.height;
        this.camera.updateProjectionMatrix();

        this.renderer.setSize(this.width, this.height);
        this.resizeOverlay();
    }

    resizeOverlay() {
        this.overlayCanvas.width = this.width;
        this.overlayCanvas.height = this.height;
    }

    onClick(e) {
        const rect = this.canvas.getBoundingClientRect();
        this.mouse.x = ((e.clientX - rect.left) / this.width) * 2 - 1;
        this.mouse.y = -((e.clientY - rect.top) / this.height) * 2 + 1;

        this.raycaster.setFromCamera(this.mouse, this.camera);
        const intersects = this.raycaster.intersectObjects(this.sensors);

        if (intersects.length > 0) {
            this.selectedSensor = intersects[0].object;
            if (this.onSensorClick) {
                this.onSensorClick(this.selectedSensor.userData);
            }
        }
    }

    onMouseMove(e) {
        const rect = this.canvas.getBoundingClientRect();
        this.mouse.x = ((e.clientX - rect.left) / this.width) * 2 - 1;
        this.mouse.y = -((e.clientY - rect.top) / this.height) * 2 + 1;

        this.raycaster.setFromCamera(this.mouse, this.camera);
        const intersects = this.raycaster.intersectObjects(this.sensors);

        this.canvas.style.cursor = intersects.length > 0 ? 'pointer' : 'grab';

        this.sensors.forEach(sensor => {
            sensor.scale.setScalar(1);
        });

        if (intersects.length > 0) {
            intersects[0].object.scale.setScalar(1.5);
        }
    }

    animate() {
        requestAnimationFrame(() => this.animate());

        if (this.controls.enabled) {
            this.controls.update();
        }

        const time = Date.now() * 0.001;
        this.sensors.forEach((sensor, index) => {
            const baseScale = sensor === this.selectedSensor ? 1.5 : 1;
            const pulse = 1 + Math.sin(time * 2 + index * 0.5) * 0.1;
            sensor.scale.setScalar(baseScale * pulse);
        });

        this.renderer.render(this.scene, this.camera);
    }

    rotateTank(angle) {
        this.tankGroup.rotation.y = angle;
    }
}
