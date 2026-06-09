#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
LNG储罐Modbus TCP模拟器
模拟4座16万立方米储罐的传感器数据
"""

import sys
import time
import math
import random
import threading
from datetime import datetime
from pyModbusTCP.server import ModbusServer, DataBank

try:
    from pyModbusTCP.server import ModbusServer, DataBank
except ImportError:
    print("请先安装依赖: pip install pyModbusTCP")
    sys.exit(1)


class LNGTankSimulator:
    def __init__(self, host='0.0.0.0', port=502):
        self.host = host
        self.port = port
        self.server = None
        self.running = False

        self.tank_count = 4
        self.layers = 5
        self.thermo_per_layer = 8
        self.density_meters = 3

        self.tank_data = {}
        for tank_id in range(1, self.tank_count + 1):
            self.tank_data[tank_id] = {
                'base_temps': self._generate_base_temps(),
                'base_densities': self._generate_base_densities(),
                'pressure': 0.15 + random.uniform(0, 0.05),
                'rollover_risk': random.uniform(0, 0.3),
                'compressor_status': [1, 1],
                'time_offset': random.uniform(0, 2 * math.pi)
            }

        self.registers = {}
        self._init_registers()

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

        temp_diff = data['base_temps'][-1][0] - data['base_temps'][0][0]
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
        for addr, values in self.registers.items():
            try:
                DataBank.set_words(addr, values)
            except Exception as e:
                print(f"设置寄存器错误: addr={addr}, error={e}")

    def _simulation_loop(self):
        start_time = time.time()
        while self.running:
            elapsed = time.time() - start_time

            for tank_id in range(1, self.tank_count + 1):
                self._update_tank_data(tank_id, elapsed)
                self._update_registers(tank_id)

            self._apply_registers_to_server()

            if int(elapsed) % 30 == 0:
                self._print_status(elapsed)

            time.sleep(1)

    def _print_status(self, elapsed):
        print(f"\n=== 模拟器状态 - {datetime.now().strftime('%Y-%m-%d %H:%M:%S')} ===")
        print(f"运行时间: {elapsed:.1f} 秒")

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

            print(f"\n储罐 T-10{tank_id}:")
            print(f"  层温度: {[f'{t:.1f}' for t in temps]} ℃")
            print(f"  密度: {[f'{d:.1f}' for d in densities]} kg/m³")
            print(f"  压力: {data['pressure']:.4f} MPa")
            print(f"  温差: {temp_diff:.2f} ℃, 密度差: {density_diff:.2f} kg/m³")
            print(f"  翻滚风险: {data['rollover_risk']*100:.1f}% ({risk_level})")
            print(f"  压缩机状态: {data['compressor_status']}")

    def induce_rollover(self, tank_id):
        """手动触发翻滚事件用于测试"""
        if tank_id in self.tank_data:
            data = self.tank_data[tank_id]
            data['rollover_risk'] = 0.9
            for layer in range(self.layers):
                for sensor in range(self.thermo_per_layer):
                    data['base_temps'][layer][sensor] = -162 + layer * 2.5
            data['base_densities'] = [426, 423, 420]
            data['pressure'] = 0.23
            print(f"\n!!! 已手动触发储罐 T-10{tank_id} 翻滚事件 !!!")

    def start(self):
        self.running = True
        self.server = ModbusServer(host=self.host, port=self.port, no_block=True)

        try:
            self.server.start()
            print(f"\nModbus TCP 服务器已启动: {self.host}:{self.port}")
            print("按 Ctrl+C 停止服务器")
            print("输入 'rollover <储罐ID>' 手动触发翻滚事件")
            print("输入 'status' 查看当前状态\n")

            sim_thread = threading.Thread(target=self._simulation_loop, daemon=True)
            sim_thread.start()

            while self.running:
                try:
                    cmd = input().strip()
                    if cmd.startswith('rollover'):
                        parts = cmd.split()
                        if len(parts) == 2:
                            tank_id = int(parts[1])
                            self.induce_rollover(tank_id)
                        else:
                            print("用法: rollover <1-4>")
                    elif cmd == 'status':
                        self._print_status(time.time() - time.time())
                    elif cmd == 'help':
                        print("可用命令:")
                        print("  rollover <1-4>  - 触发指定储罐翻滚事件")
                        print("  status          - 显示当前状态")
                        print("  help            - 显示帮助")
                        print("  exit/quit       - 退出模拟器")
                    elif cmd in ('exit', 'quit'):
                        break
                except KeyboardInterrupt:
                    break
                except Exception as e:
                    print(f"命令错误: {e}")

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
        print("\nModbus TCP 服务器已停止")


def main():
    import argparse

    parser = argparse.ArgumentParser(description='LNG储罐Modbus TCP模拟器')
    parser.add_argument('--host', default='0.0.0.0', help='监听地址')
    parser.add_argument('--port', type=int, default=502, help='监听端口')
    args = parser.parse_args()

    if args.port < 1024 and os.name != 'nt':
        print("警告: 在Linux系统上，端口<1024需要root权限")
        print("可以使用 sudo 运行，或指定 >1024 的端口，例如 --port 5020")

    simulator = LNGTankSimulator(host=args.host, port=args.port)
    simulator.start()


if __name__ == '__main__':
    import os
    main()
