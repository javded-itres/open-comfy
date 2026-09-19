import time
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8788/v1", api_key="sk-...", timeout=200.0)
job = client.videos.create(model="minimax-hailuo-02", prompt="waves at sunset", seconds="6")
while job.status not in ("completed", "failed", "cancelled"):
    time.sleep(3)
    job = client.videos.retrieve(job.id)
print(job.status, getattr(job, "url", None))
if job.status == "completed":
    content = client.videos.download_content(job.id)
    print(content)
