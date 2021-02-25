# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.

FROM python:3.7-slim-buster

WORKDIR /app
COPY . . 

RUN pip install -r requirements.txt

EXPOSE 3000
ENTRYPOINT ["python"]
CMD ["flask_service.py"]
