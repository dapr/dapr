import time
import requests
import os

actions_url = "http://localhost:3500/publish"

while True:
  message = { "eventName": "neworder", "data": { "orderID": "777" }, "to": ["nodeapp"] }

  try:
    response = requests.post(actions_url, json=message)
  except Exception as e:
      print(e)

  time.sleep(1)
  
