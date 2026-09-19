from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8788/v1", api_key="sk-...", timeout=200.0)
img = client.images.generate(model="flux-dev", prompt="neon city, 16:9")
print(img.data[0].b64_json is not None or img.data[0].url)
