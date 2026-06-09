#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
LNG储罐Modbus TCP模拟器（增强版）
支持4座储罐，每座43个传感器（40温度+3密度）
30秒更新间隔，可注入温度密度分层和翻滚条件
HTTP API控制接口
"""

import sys
import os
import time
import math
import random
import threading
import json
import argparse
from datetime import datetime
from http.server import HTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs

try:
    from pyModbusTCP.server import ModbusServer, DataBank
except ImportError:
    print("请先安装依赖: pip install pyModbusTCP")
    sys.exit(1)


class APIServer(BaseHTTPRequestHandler):
    simulator = None

    def do_GET(self):
        parsed = urlparse(self.path)
        params = parse_qs(parsed.query)

        if parsed.path == '/api/status':
            response = self.simulator.get_status()
            self._send_json_response(response)
        elif parsed.path == '/api/induce_rollover':
            tank_id = int(params.get('tank_id', [1])[0])
            result = self.simulator.induce_rollover(tank_id)
            self._send_json_response(result)
        elif parsed.path == '/api/inject_stratification':
            tank_id = int(params.get('tank_id', [1])[0])
            temp_diff = float(params.get('temp_diff', [10.0])[0])
            density_diff = float(params.get('density_diff', [3.0])[0])
            result = self.simulator.inject_stratification(tank_id, temp_diff, density_diff)
            self._send_json_response(result)
        elif parsed.path == '/api/reset_tank':
            tank_id = int(params.get('tank_id', [1])[0])
            result = self.simulator.reset_tank(tank_id)
            self._send_json_response(result)
        elif parsed.path == '/api/set_interval':
            interval = int(params.get('seconds', [30])[0])
            result = self.simulator.set_update_interval(interval)
            self._send_json_response(result)
        elif parsed.path == '/health':
            self._send_json_response({'status': 'healthy', 'timestamp': time.time()})
        else:
            self._send_json_response({'error': 'Not found'}, 404)

    def do_POST(self):
        parsed = urlparse(self.path)
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length) if content_length > 0 else b'{}'
        data = json.loads(body) if body else {}

        if parsed.path == '/api/induce_rollover':
            tank_id = data.get('tank_id', 1)
            result = self.simulator.induce_rollover(tank_id)
            self._send_json_response(result)
        elif parsed.path == '/api/inject_stratification':
            tank_id = data.get('tank_id', 1)
            temp_diff = data.get('temp_diff', 10.0)
            density_diff = data.get('density_diff', 3.0)
            result = self.simulator.inject_stratification(tank_id, temp_diff, density_diff)
            self._send_json_response(result)
        else:
            self._send_json_response({'error': 'Not found'}, 404)

    def _send_json_response(self, data, status=200):
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()
        self.wfile.write(json.dumps(data, indent=2).encode('utf-8'))

    def log_message(self, format, *args):
        pass


class LNGTankSimulator:
    def __init__(self, host='0.0.0.0', port=502, api_port=8000, update_interval=30):
        self.host = host
        self.port = port
        self.api_port = api_port
        self.update_interval = update_interval
        self.server = None
        self.api_server = None
        self.running = False

        self.tank_count = 4
        self.layers = 5
        self.thermo_per_layer = 8
        self.density_meters = 3
        self.sensors_per_tank = self.layers * self.thermo_per_layer + self.density_meters

        self.tank_data = {}
        for tank_id in range(1, self.tank_count + 1):
            self.tank_data[tank_id] = self._init_tank_data()

        self.registers = {}
        self._init_registers()
        self._lock = threading.Lock()

    def _init_tank_data(self):
        return {
            'base_temps': self._generate_base_temps(),
            'base_densities': self._generate_base_densities(),
            'pressure': 0.15 + random.uniform(0, 0.05),
            'rollover_risk': random.uniform(0, 0.3),
            'compressor_status': [1, 1],
            'time_offset': random.uniform(0, 2 * math.pi),
            'stratification_injected': False,
            'rollover_induced': False,
        }

    def _generate_base_temps(self):
        temps = []
        base_temp = -162.0
        for i in range(self.layers):
            layer_temps = []
            for j in range(self.thermo_per_layer):
                temp = base_temp + i * 1.2 + random.uniform(-0.3, 0.3)
                layer_temps.append(temp)
            temps.append(layer_temps)
        return temps

    def _generate_base_densities(self):
        densities = []
        base_density = 425.0
        for i in range(self.density_meters):
            density = base_density - i * 0.8 + random.uniform(-0.2, 0.2)
            densities.append(density)
        return densities

    def _init_registers(self):
        for tank_id in range(1, self.tank_count + 1):
            base_addr = (tank_id - 1) * 1000

            for layer in range(self.layers):
                for sensor in range(self.thermo_per_layer):
                    addr = base_addr + layer * self.thermo_per_layer + sensor
                    self.registers[addr] = self._float_to_registers(-162.0)

            for sensor in range(self.density_meters):
                addr = base_addr + 500 + sensor
                self.registers[addr] = self._float_to_registers(425.0)

            self.registers[base_addr + 600] = self._float_to_registers(0.15)

            for comp in range(2):
                status_addr = base_addr + 700 + comp * 10
                self.registers[status_addr] = [1]
                self.registers[status_addr + 1] = self._float_to_registers(1.5)
                self.registers[status_addr + 2] = self._float_to_registers(45.0)
                self.registers[status_addr + 3] = self._float_to_registers(0.12)

    def _float_to_registers(self, value):
        import struct
        packed = struct.pack('>f', value)
        return [struct.unpack('>H', packed[0:2])[0], struct.unpack('>H', packed[2:4])[0]]

    def _update_tank_data(self, tank_id, elapsed_time):
        with self._lock:
            data = self.tank_data[tank_id]

            for layer in range(self.layers):
                for sensor in range(self.thermo_per_layer):
                    base = data['base_temps'][layer][sensor]
                    wave = math.sin(elapsed_time * 0.01 + data['time_offset'] + layer * 0.5) * 0.5
                    drift = elapsed_time * 0.0001 * (1 + data['rollover_risk'])
                    noise = random.uniform(-0.1, 0.1)

                    if data['rollover_risk'] > 0.7:
                        drift *= 3
                        wave *= 2

                    temp = base + wave + drift + noise
                    data['base_temps'][layer][sensor] = max(-170, min(-150, temp))

            for sensor in range(self.density_meters):
                base = data['base_densities'][sensor]
                wave = math.sin(elapsed_time * 0.008 + data['time_offset'] + sensor) * 0.3
                drift = -elapsed_time * 0.00005 * (1 + data['rollover_risk'])
                noise = random.uniform(-0.05, 0.05)

                density = base + wave + drift + noise
                data['base_densities'][sensor] = max(415, min(430, density))

            temps_flat = [data['base_temps'][i][0] for i in range(self.layers)]
            temp_diff = max(temps_flat) - min(temps_flat)
            density_diff = data['base_densities'][0] - data['base_densities'][-1]

            if temp_diff > 8 and density_diff > 2:
                data['rollover_risk'] = min(1.0, data['rollover_risk'] + 0.01)
            else:
                data['rollover_risk'] = max(0, data['rollover_risk'] - 0.005)

            base_pressure = 0.15 + data['rollover_risk'] * 0.1
            data['pressure'] = base_pressure + math.sin(elapsed_time * 0.02) * 0.005

            for comp in range(2):
                if data['rollover_risk'] > 0.5 or data['pressure'] > 0.22:
                    data['compressor_status'][comp] = 1
                elif elapsed_time % 3600 < 1800:
                    data['compressor_status'][comp] = 1
                else:
                    data['compressor_status'][comp] = 0

    def _update_registers(self, tank_id):
        with self._lock:
            data = self.tank_data[tank_id]
            base_addr = (tank_id - 1) * 1000

            for layer in range(self.layers):
                for sensor in range(self.thermo_per_layer):
                    addr = base_addr + layer * self.thermo_per_layer + sensor
                    temp = data['base_temps'][layer][sensor]
                    self.registers[addr] = self._float_to_registers(temp)

            for sensor in range(self.density_meters):
                addr = base_addr + 500 + sensor
                density = data['base_densities'][sensor]
                self.registers[addr] = self._float_to_registers(density)

            self.registers[base_addr + 600] = self._float_to_registers(data['pressure'])

            for comp in range(2):
                status_addr = base_addr + 700 + comp * 10
                status = data['compressor_status'][comp]
                self.registers[status_addr] = [status]

                vibration = 1.0 + random.uniform(0, 2.0) if status else 0.1
                current = 40.0 + random.uniform(0, 20.0) if status else 0.5
                discharge = 0.1 + random.uniform(0, 0.05) if status else 0.01

                self.registers[status_addr + 1] = self._float_to_registers(vibration)
                self.registers[status_addr + 2] = self._float_to_registers(current)
                self.registers[status_addr + 3] = self._float_to_registers(discharge)

    def _apply_registers_to_server(self):
        with self._lock:
            for addr, values in self.registers.items():
                try:
                    DataBank.set_words(addr, values)
                except Exception as e:
                    pass

    def _simulation_loop(self):
        start_time = time.time()
        last_update = 0

        while self.running:
            elapsed = time.time() - start_time

            if elapsed - last_update >= self.update_interval:
                last_update = elapsed

                for tank_id in range(1, self.tank_count + 1):
                    self._update_tank_data(tank_id, elapsed)
                    self._update_registers(tank_id)

                self._apply_registers_to_server()

                if int(elapsed) % 60 == 0:
                    self._print_status(elapsed)

            time.sleep(1)

    def _print_status(self, elapsed):
        print(f"\n=== 模拟器状态 - {datetime.now().strftime('%Y-%m-%d %H:%M:%S')} ===")
        print(f"运行时间: {elapsed:.1f} 秒, 更新间隔: {self.update_interval}s")

        for tank_id in range(1, self.tank_count + 1):
            data = self.tank_data[tank_id]
            temps = [data['base_temps'][i][0] for i in range(self.layers)]
            densities = data['base_densities']
            temp_diff = max(temps) - min(temps)
            density_diff = densities[0] - densities[-1]

            risk_level = "低"
            if data['rollover_risk'] > 0.7:
                risk_level = "高"
            elif data['rollover_risk'] > 0.4:
                risk_level = "中"

            flags = []
            if data['stratification_injected']:
                flags.append("分层注入")
            if data['rollover_induced']:
                flags.append("翻滚触发")
            flag_str = f" [{', '.join(flags)}]" if flags else ""

            print(f"\n储罐 T-10{tank_id}{flag_str}:")
            print(f"  层温度: {[f'{t:.1f}' for t in temps]} ℃")
            print(f"  密度: {[f'{d:.1f}' for d in densities]} kg/m³")
            print(f"  压力: {data['pressure']:.4f} MPa")
            print(f"  温差: {temp_diff:.2f} ℃, 密度差: {density_diff:.2f} kg/m³")
            print(f"  翻滚风险: {data['rollover_risk']*100:.1f}% ({risk_level})")
            print(f"  压缩机状态: {data['compressor_status']}")

    def get_status(self):
        status = {
            'timestamp': time.time(),
            'update_interval': self.update_interval,
            'tank_count': self.tank_count,
            'sensors_per_tank': self.sensors_per_tank,
            'tanks': {}
        }

        for tank_id in range(1, self.tank_count + 1):
            data = self.tank_data[tank_id]
            temps = [data['base_temps'][i][0] for i in range(self.layers)]
            status['tanks'][f'T-10{tank_id}'] = {
                'tank_id': tank_id,
                'temperatures': data['base_temps'],
                'densities': data['base_densities'],
                'pressure': data['pressure'],
                'rollover_risk': data['rollover_risk'],
                'compressor_status': data['compressor_status'],
                'stratification_injected': data['stratification_injected'],
                'rollover_induced': data['rollover_induced'],
                'temp_diff': max(temps) - min(temps),
                'density_diff': data['base_densities'][0] - data['base_densities'][-1],
            }

        return status

    def inject_stratification(self, tank_id, temp_diff=10.0, density_diff=3.0):
        if tank_id not in self.tank_data:
            return {'success': False, 'error': f'Tank {tank_id} not found'}

        with self._lock:
            data = self.tank_data[tank_id]
            base_temp = -162.0
            for layer in range(self.layers):
                temp = base_temp + layer * (temp_diff / (self.layers - 1))
                for sensor in range(self.thermo_per_layer):
                    data['base_temps'][layer][sensor] = temp + random.uniform(-0.2, 0.2)

            base_density = 426.0
            for i in range(self.density_meters):
                data['base_densities'][i] = base_density - i * (density_diff / (self.density_meters - 1))

            data['stratification_injected'] = True
            data['rollover_risk'] = 0.6

        print(f"\n!!! 已注入分层条件到储罐 T-10{tank_id}: 温差={temp_diff}℃, 密度差={density_diff}kg/m³ !!!")
        return {
            'success': True,
            'tank_id': tank_id,
            'temp_diff': temp_diff,
            'density_diff': density_diff,
            'message': f'Stratification injected to T-10{tank_id}'
        }

    def induce_rollover(self, tank_id):
        if tank_id not in self.tank_data:
            return {'success': False, 'error': f'Tank {tank_id} not found'}

        with self._lock:
            data = self.tank_data[tank_id]
            data['rollover_risk'] = 0.95
            data['rollover_induced'] = True

            for layer in range(self.layers):
                for sensor in range(self.thermo_per_layer):
                    data['base_temps'][layer][sensor] = -162 + layer * 2.5

            data['base_densities'] = [426, 423, 420]
            data['pressure'] = 0.24

        print(f"\n!!! 已手动触发储罐 T-10{tank_id} 翻滚事件 !!!")
        return {
            'success': True,
            'tank_id': tank_id,
            'message': f'Rollover induced for T-10{tank_id}'
        }

    def reset_tank(self, tank_id):
        if tank_id not in self.tank_data:
            return {'success': False, 'error': f'Tank {tank_id} not found'}

        with self._lock:
            self.tank_data[tank_id] = self._init_tank_data()

        print(f"\n已重置储罐 T-10{tank_id}")
        return {
            'success': True,
            'tank_id': tank_id,
            'message': f'Tank T-10{tank_id} reset'
        }

    def set_update_interval(self, seconds):
        if seconds < 1 or seconds > 3600:
            return {'success': False, 'error': 'Interval must be between 1 and 3600 seconds'}

        self.update_interval = seconds
        print(f"\n更新间隔已设置为 {seconds} 秒")
        return {
            'success': True,
            'interval': seconds,
            'message': f'Update interval set to {seconds}s'
        }

    def _start_api_server(self):
        APIServer.simulator = self
        self.api_server = HTTPServer(('0.0.0.0', self.api_port), APIServer)
        print(f"\nHTTP API 服务器已启动: http://0.0.0.0:{self.api_port}")
        print("API端点:")
        print("  GET  /api/status              - 获取所有储罐状态")
        print("  GET  /api/health              - 健康检查")
        print("  GET  /api/induce_rollover?tank_id=1          - 触发翻滚")
        print("  GET  /api/inject_stratification?tank_id=1&temp_diff=10&density_diff=3")
        print("  GET  /api/reset_tank?tank_id=1                - 重置储罐")
        print("  GET  /api/set_interval?seconds=30             - 设置更新间隔")
        self.api_server.serve_forever()

    def start(self):
        self.running = True
        self.server = ModbusServer(host=self.host, port=self.port, no_block=True)

        try:
            self.server.start()
            print(f"\nModbus TCP 服务器已启动: {self.host}:{self.port}")
            print(f"储罐数量: {self.tank_count}, 每罐传感器: {self.sensors_per_tank}")
            print(f"温度传感器: {self.layers}层 × {self.thermo_per_layer} = {self.layers * self.thermo_per_layer}")
            print(f"密度计: {self.density_meters}")
            print(f"更新间隔: {self.update_interval}秒")
            print("按 Ctrl+C 停止服务器\n")

            sim_thread = threading.Thread(target=self._simulation_loop, daemon=True)
            sim_thread.start()

            api_thread = threading.Thread(target=self._start_api_server, daemon=True)
            api_thread.start()

            while self.running:
                try:
                    time.sleep(1)
                except KeyboardInterrupt:
                    break

        except Exception as e:
            print(f"服务器错误: {e}")
        finally:
            self.stop()

    def stop(self):
        self.running = False
        if self.server:
            try:
                self.server.stop()
            except:
                pass
        if self.api_server:
            try:
                self.api_server.shutdown()
            except:
                pass
        print("\nModbus TCP 服务器已停止")


def main():
    parser = argparse.ArgumentParser(description='LNG储罐Modbus TCP模拟器（增强版）')
    parser.add_argument('--host', default=os.getenv('MODBUS_HOST', '0.0.0.0'), help='监听地址')
    parser.add_argument('--port', type=int, default=int(os.getenv('MODBUS_PORT', 502)), help='监听端口')
    parser.add_argument('--api-port', type=int, default=int(os.getenv('API_PORT', 8000)), help='API端口')
    parser.add_argument('--interval', type=int, default=int(os.getenv('UPDATE_INTERVAL', 30)), help='更新间隔(秒)')
    args = parser.parse_args()

    if args.port < 1024 and os.name != 'nt':
        print("警告: 在Linux系统上，端口<1024需要root权限")
        print("可以使用 sudo 运行，或指定 >1024 的端口，例如 --port 5020")

    simulator = LNGTankSimulator(
        host=args.host,
        port=args.port,
        api_port=args.api_port,
        update_interval=args.interval
    )
    simulator.start()


if __name__ == '__main__':
    main()
