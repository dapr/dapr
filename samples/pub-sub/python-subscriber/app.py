import flask
from flask import request, jsonify
from flask_cors import CORS
import json
import sys

app = flask.Flask(__name__)
CORS(app)

@app.route('/A', methods=['POST'])
def a():
    content = request.json
    print(content, file=sys.stderr)
    return json.dumps({'success':True}), 200, {'ContentType':'application/json'} 

@app.route('/C', methods=['POST'])
def b():
    content = request.json
    print(content, file=sys.stderr)
    return json.dumps({'success':True}), 200, {'ContentType':'application/json'} 

@app.route('/actions/subscribe', methods=['GET'])
def subscribe():
    return jsonify(['A','C'])
app.run()