import flask
from flask import request, jsonify
from flask_cors import CORS
import math
import sys

app = flask.Flask(__name__)
CORS(app)

@app.route('/multiply', methods=['POST'])
def multiply():
    content = request.json
    [operand_one, operand_two] = [float(content['operandOne']), float(content['operandTwo'])]
    print(f"Calculating {operand_one} * {operand_two}", flush=True)
    return jsonify(math.ceil(operand_one * operand_two * 100000)/100000)

app.run()