#!/bin/bash
exec python3 -c "
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        self.rfile.read(n)
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps({
            'id': 'chatcmpl-mock', 'object': 'chat.completion', 'model': 'mock-model',
            'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': 'hi'}}],
            'usage': {'prompt_tokens': 12, 'completion_tokens': 8, 'total_tokens': 20}
        }).encode())
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', 9999), H).serve_forever()
"
