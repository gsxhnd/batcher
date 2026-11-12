package presets

import "slices"

// Builtin 一条内置转码预设：Name 为界面上的说明，Args 为传给 ffmpeg 的高阶参数。
type Builtin struct {
	Name string
	Args string
}

// builtinPresets 是代码内置的常用转码参数。第一次打开且库里还没有预设时写入数据库，之后与其他记录一样可以改、可以删。
// 参数均假设 ffmpeg 已正确安装；视频流重编码时音频轨保持 copy 以减少耗损。
var builtinPresets = []Builtin{
	{Name: "无损封装（仅换容器）", Args: "-c copy"},
	{Name: "H.264 通用", Args: "-c:v libx264 -preset fast -crf 23 -c:a copy"},
	{Name: "H.264 高质量", Args: "-c:v libx264 -preset slow -crf 18 -c:a copy"},
	{Name: "H.265 通用", Args: "-c:v libx265 -preset fast -crf 26 -c:a copy"},
	{Name: "H.265 10bit", Args: "-c:v libx265 -preset fast -crf 24 -pix_fmt yuv420p10le -c:a copy"},
	{Name: "NVIDIA H.264", Args: "-c:v h264_nvenc -preset fast -cq 23 -c:a copy"},
	{Name: "NVIDIA H.265", Args: "-c:v hevc_nvenc -preset fast -cq 26 -c:a copy"},
	{Name: "Apple H.264 (VideoToolbox)", Args: "-c:v h264_videotoolbox -q:v 65 -c:a copy"},
	{Name: "Apple H.265 (VideoToolbox)", Args: "-c:v hevc_videotoolbox -q:v 65 -c:a copy"},
}

// Builtins 返回内置预设的副本，调用方修改副本不影响内置列表。
func Builtins() []Builtin {
	return slices.Clone(builtinPresets)
}
