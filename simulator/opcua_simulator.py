#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
LNG储罐OPC UA模拟器
模拟DCS系统的OPC UA服务器，接收告警并展示
"""

import sys
import os
import time
import threading
import json
import argparse
from datetime import datetime
from http.server import HTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs

try:
    from opcua import Server, ua
except ImportError:
    print("请先安装依赖: pip install opcua")
    sys.exit(1)


class APIHandler(BaseHTTPRequestHandler):
    server_instance = None

    def do_GET(self):
        parsed = urlparse(self.path)
        params = parse_qs(parsed.query)

        if parsed.path == '/api/alarms':
            response = self.server_instance.get_alarms()
            self._send_json_response(response)
        elif parsed.path == '/api/clear_alarm':
            alarm_id = int(params.get('alarm_id', [0])[0])
            result = self.server_instance.clear_alarm(alarm_id)
            self._send_json_response(result)
        elif parsed.path == '/api/status':
            response = self.server_instance.get_status()
            self._send_json_response(response)
        elif parsed.path == '/health':
            self._send_json_response({'status': 'healthy', 'timestamp': time.time()})
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


class OPCUASimulator:
    def __init__(self, endpoint='opc.tcp://0.0.0.0:4840', api_port=8001):
        self.endpoint = endpoint
        self.api_port = api_port
        self.server = None
        self.running = False
        self.alarms = []
        self.alarm_counter = 0
        self.nodes = {}
        self._lock = threading.Lock()

    def start(self):
        self.running = True
        self.server = Server()
        self.server.set_endpoint(self.endpoint)
        self.server.set_server_name("LNG DCS OPC UA Server")

        uri = "http://lng-dcs.example.com"
        idx = self.server.register_namespace(uri)

        objects = self.server.get_objects_node()

        lng_system = objects.add_object(idx, "LNGSystem")

        tanks_obj = lng_system.add_object(idx, "Tanks")
        self.nodes['tanks'] = tanks_obj

        alarms_obj = lng_system.add_object(idx, "Alarms")
        self.nodes['alarms'] = alarms_obj

        for tank_id in range(1, 5):
            tank_obj = tanks_obj.add_object(idx, f"Tank_{tank_id}")
            self.nodes[f'tank_{tank_id}'] = tank_obj

            self.nodes[f'tank_{tank_id}_risk'] = tank_obj.add_variable(
                idx, "RolloverRisk", 0.0
            )
            self.nodes[f'tank_{tank_id}_risk'].set_writable()

            self.nodes[f'tank_{tank_id}_temp_diff'] = tank_obj.add_variable(
                idx, "TemperatureDiff", 0.0
            )
            self.nodes[f'tank_{tank_id}_temp_diff'].set_writable()

            self.nodes[f'tank_{tank_id}_density_diff'] = tank_obj.add_variable(
                idx, "DensityDiff", 0.0
            )
            self.nodes[f'tank_{tank_id}_density_diff'].set_writable()

            self.nodes[f'tank_{tank_id}_pressure'] = tank_obj.add_variable(
                idx, "Pressure", 0.0
            )
            self.nodes[f'tank_{tank_id}_pressure'].set_writable()

            alarm_var = alarms_obj.add_variable(
                idx, f"Tank_{tank_id}_Alarm", ""
            )
            alarm_var.set_writable()
            self.nodes[f'alarm_{tank_id}'] = alarm_var

        self.nodes['system_status'] = lng_system.add_variable(
            idx, "SystemStatus", "Normal"
        )
        self.nodes['system_status'].set_writable()

        self.nodes['active_alarms_count'] = lng_system.add_variable(
            idx, "ActiveAlarmsCount", 0
        )
        self.nodes['active_alarms_count'].set_writable()

        self.server.start()
        print(f"\nOPC UA 服务器已启动: {self.endpoint}")
        print(f"命名空间索引: {idx}")
        print("对象结构:")
        print("  LNGSystem/")
        print("    ├─ Tanks/")
        for i in range(1, 5):
            print(f"    │   └─ Tank_{i}/")
            print("    │       ├─ RolloverRisk")
            print("    │       ├─ TemperatureDiff")
            print("    │       ├─ DensityDiff")
            print("    │       └─ Pressure")
        print("    ├─ Alarms/")
        for i in range(1, 5):
            print(f"    │   └─ Tank_{i}_Alarm")
        print("    ├─ SystemStatus")
        print("    └─ ActiveAlarmsCount\n")

        api_thread = threading.Thread(target=self._start_api_server, daemon=True)
        api_thread.start()

        status_thread = threading.Thread(target=self._status_printer, daemon=True)
        status_thread.start()

        try:
            while self.running:
                time.sleep(1)
        except KeyboardInterrupt:
            pass
        finally:
            self.stop()

    def _start_api_server(self):
        APIHandler.server_instance = self
        self.api_server = HTTPServer(('0.0.0.0', self.api_port), APIHandler)
        print(f"HTTP API 服务器已启动: http://0.0.0.0:{self.api_port}")
        print("API端点:")
        print("  GET  /api/status              - 获取服务器状态")
        print("  GET  /api/alarms              - 获取所有告警")
        print("  GET  /api/clear_alarm?id=1    - 清除指定告警")
        print("  GET  /health                  - 健康检查\n")
        self.api_server.serve_forever()

    def _status_printer(self):
        while self.running:
            time.sleep(30)
            with self._lock:
                active_count = len([a for a in self.alarms if not a.get('cleared')])
            print(f"[{datetime.now().strftime('%H:%M:%S')}] OPC UA Server - 活跃告警: {active_count}, 总告警: {len(self.alarms)}")

    def get_status(self):
        with self._lock:
            return {
                'timestamp': time.time(),
                'endpoint': self.endpoint,
                'total_alarms': len(self.alarms),
                'active_alarms': len([a for a in self.alarms if not a.get('cleared')]),
                'system_status': self.nodes['system_status'].get_value()
            }

    def get_alarms(self):
        with self._lock:
            return {
                'alarms': self.alarms[-100:],
                'total': len(self.alarms)
            }

    def clear_alarm(self, alarm_id):
        with self._lock:
            for alarm in self.alarms:
                if alarm['id'] == alarm_id:
                    alarm['cleared'] = True
                    alarm['cleared_at'] = datetime.now().isoformat()

                    tank_id = alarm.get('tank_id')
                    if tank_id and f'alarm_{tank_id}' in self.nodes:
                        self.nodes[f'alarm_{tank_id}'].set_value("")

                    self._update_active_count()

                    return {
                        'success': True,
                        'alarm_id': alarm_id,
                        'message': f'Alarm {alarm_id} cleared'
                    }
            return {'success': False, 'error': f'Alarm {alarm_id} not found'}

    def _update_active_count(self):
        active = len([a for a in self.alarms if not a.get('cleared')])
        self.nodes['active_alarms_count'].set_value(active)

        if active > 0:
            self.nodes['system_status'].set_value("Warning")
        else:
            self.nodes['system_status'].set_value("Normal")

    def stop(self):
        self.running = False
        if self.server:
            try:
                self.server.stop()
            except:
                pass
        print("\nOPC UA 服务器已停止")


def main():
    parser = argparse.ArgumentParser(description='LNG储罐OPC UA模拟器')
    parser.add_argument('--host', default=os.getenv('OPCUA_HOST', '0.0.0.0'), help='监听地址')
    parser.add_argument('--port', type=int, default=int(os.getenv('OPCUA_PORT', 4840)), help='监听端口')
    parser.add_argument('--api-port', type=int, default=int(os.getenv('API_PORT', 8001)), help='API端口')
    args = parser.parse_args()

    endpoint = f'opc.tcp://{args.host}:{args.port}'

    simulator = OPCUASimulator(endpoint=endpoint, api_port=args.api_port)
    simulator.start()


if __name__ == '__main__':
    main()
