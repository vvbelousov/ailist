"""Exercise the actual faster-whisper audio decoder without downloading weights."""
import tempfile
import unittest
import wave

from faster_whisper.audio import decode_audio


class DecoderTest(unittest.TestCase):
    def test_pcm_wav(self):
        with tempfile.NamedTemporaryFile() as audio:
            with wave.open(audio.name, "wb") as wav:
                wav.setparams((1, 2, 16000, 0, "NONE", ""))
                wav.writeframes(bytes(32000))
            self.assertEqual(len(decode_audio(audio.name)), 16000)


if __name__ == "__main__":
    unittest.main()
