from litellm import image_generation

image_generation(
    model="openai/flux-dev",
    prompt="a red cube",
    api_base="http://127.0.0.1:8788/v1",
    api_key="sk-...",
)
