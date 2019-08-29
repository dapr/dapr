FROM python:3.7.1-alpine3.8
COPY . /app
WORKDIR /app
RUN pip install requests
ENTRYPOINT ["python"]
CMD ["app.py"]
