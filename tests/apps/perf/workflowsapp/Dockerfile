
FROM python:3.9-slim-buster

WORKDIR /app

COPY . .

RUN pip3 install --no-cache-dir -r requirements.txt gunicorn

#CMD ["python3", "app.py"]

CMD ["gunicorn", \
     "--bind", "0.0.0.0:3000", \
     "--workers", "16", \
     "--threads", "32", \
     "--worker-class", "gthread", \
     "--timeout", "180", \
     "--max-requests", "1000", \
     "--max-requests-jitter", "100", \
     "--log-level", "warning", \
     "app:api"]