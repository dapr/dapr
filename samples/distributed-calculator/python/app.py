import flask
from flask import request, jsonify
from flask_cors import CORS
import math

app = flask.Flask(__name__)
CORS(app)

@app.route('/multiply', methods=['POST'])
def api_id():
    content = request.json
    [operand_one, operand_two] = [float(content['operandOne']), float(content['operandTwo'])]
    print(f"Calculating {operand_one} * {operand_two}")
    return jsonify(math.ceil(operand_one * operand_two * 100000)/100000)

app.run()