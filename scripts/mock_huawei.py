#!/usr/bin/env python3
"""
Mock Huawei Push Kit Server
Simulates the local endpoint for testing HarmonyOS push integration.
"""

import http.server
import json
import sys

class MockHuaweiPushHandler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        # Read request body
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length).decode('utf-8')
        
        try:
            request_data = json.loads(body)
            sys.stderr.write(f"\n[Mock Server] Received push request:\n")
            sys.stderr.write(f"  - URL: {self.path}\n")
            sys.stderr.write(f"  - Authorization: {self.headers.get('Authorization', 'NOT PROVIDED')[:30]}...\n")
            sys.stderr.write(f"  - Payload: {json.dumps(request_data, indent=2)[:200]}...\n")
            sys.stderr.flush()
            
            # Respond with success
            response = {
                "code": 80000000,
                "msg": "Success",
                "requestId": "mock-request-id-" + str(self.server.server_address[1])
            }
            
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps(response).encode('utf-8'))
            
        except json.JSONDecodeError:
            sys.stderr.write(f"[Mock Server] Invalid JSON: {body}\n")
            sys.stderr.flush()
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b'{"code": 400, "msg": "Invalid JSON"}')
    
    def log_message(self, format, *args):
        # Suppress default access log to stderr
        pass

def run_server(port=9999):
    server_address = ('', port)
    httpd = http.server.HTTPServer(server_address, MockHuaweiPushHandler)
    sys.stderr.write(f"🚀 Mock Huawei Push Server running on port {port}...\n")
    sys.stderr.write(f"   Accepting all POST requests and returning success\n")
    sys.stderr.write(f"   Press Ctrl+C to stop\n")
    sys.stderr.flush()
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        sys.stderr.write("\n🛑 Mock server stopped.\n")
        sys.stderr.flush()
        httpd.server_close()

if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9999
    run_server(port)
