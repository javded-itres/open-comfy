package importwf

import "testing"

func TestModelFolderLayouts(t *testing.T) {
cases := []struct {
class, field, want string
}{
{"UNet", "unet_name", "diffusion_models"},
{"SomeModel", "diffusion_model", "diffusion_models"},
{"TextEncoder", "clip", "text_encoders"},
{"T5TextEncoder", "text_encoder", "text_encoders"},
{"VAEDecoder", "vae", "vae"},
{"LoRA", "lora", "loras"},
{"ControlNet", "controlnet", "controlnet"},
{"Checkpoint", "model", "checkpoints"},
}
for _, c := range cases {
if got := modelFolder(c.class, c.field); got != c.want {
t.Errorf("modelFolder(%q,%q) = %q, want %q", c.class, c.field, got, c.want)
}
}
}
