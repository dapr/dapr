import time
import requests
import os

actions_url = "http://localhost:3500/v1.0/invoke/nodeapp/method/neworder"
n = 0
while True:
    n += 1
    message = {"data": {"orderId": n}}

    try:
        response = requests.post(actions_url, json=message)
    except Exception as e:
        print(e)

    time.sleep(1)
