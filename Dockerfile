FROM python:3.14-slim

WORKDIR /app

COPY requirements.txt .
RUN python -m pip install --no-cache-dir -r requirements.txt

COPY orchestrator/ ./orchestrator/
COPY run.py .
COPY config.example.yaml .

ENV WARM_POOL_CONFIG=/app/config.example.yaml \
    WARM_POOL_HOST=0.0.0.0 \
    WARM_POOL_PORT=9090

EXPOSE 9090

CMD ["python", "run.py"]
