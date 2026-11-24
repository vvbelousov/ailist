"""Private, single-worker CPU transcription endpoint; request body is an audio file."""
import os
import tempfile
import threading

from fastapi import FastAPI, HTTPException, Request
from faster_whisper import WhisperModel
from starlette.concurrency import run_in_threadpool

app = FastAPI(docs_url=None, redoc_url=None)
slot = threading.BoundedSemaphore(1)
model = WhisperModel(os.getenv("WHISPER_MODEL", "tiny"), device="cpu", compute_type="int8",
                     cpu_threads=int(os.getenv("WHISPER_THREADS", "2")))


def recognize(filename):
    import av
    # Inspect decoded duration incrementally before Whisper allocates a waveform.
    with av.open(filename) as container:
        seconds = 0
        for frame in container.decode(audio=0):
            seconds += frame.samples / frame.sample_rate
            if seconds > 120:
                raise ValueError("audio exceeds two minutes")
    segments, _ = model.transcribe(filename, beam_size=1, vad_filter=True)
    return " ".join(segment.text.strip() for segment in segments).strip()


@app.post("/transcribe")
async def transcribe(request: Request):
    if not slot.acquire(blocking=False):
        raise HTTPException(429, "busy")
    try:
        with tempfile.NamedTemporaryFile() as audio:
            async for chunk in request.stream():
                if audio.tell() + len(chunk) > 12 * 1024 * 1024:
                    raise HTTPException(413, "audio too large")
                audio.write(chunk)
            audio.flush()
            try:
                return {"text": await run_in_threadpool(recognize, audio.name)}
            except (ValueError, OSError, IndexError) as exc:
                raise HTTPException(422, "invalid audio or duration") from exc
    finally:
        slot.release()
