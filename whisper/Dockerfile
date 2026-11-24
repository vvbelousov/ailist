FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir faster-whisper==1.2.1 av==15.1.0 fastapi==0.116.1 uvicorn==0.35.0 && useradd -m -u 10001 whisper && mkdir /models && chown whisper /models
ENV HF_HOME=/models
COPY server.py test_decode.py .
USER whisper
RUN python test_decode.py
CMD ["uvicorn","server:app","--host","0.0.0.0","--port","8000"]
