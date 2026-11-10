# ffmpeg 参数速查

> 系统环境中需要预先安装 ffmpeg

batcher 中勾选"视频转码"后填写/选择的"高级自定义参数"会解析为独立参数，放在所有输入之后、输出路径之前。支持引号和转义，不经 shell 执行。以下是常用组合参考。

## CPU 编码（libx264 / libx265）

```shell
# h264 通用（速度与质量均衡）
ffmpeg -i INPUT.mp4 -c:v libx264 -preset fast -crf 23 OUTPUT.mkv
# h264 高质量（慢）
ffmpeg -i INPUT.mp4 -c:v libx264 -preset slow -crf 18 OUTPUT.mkv
# h265 CPU 10bit 转码
ffmpeg -i INPUT.mp4 -c:v libx265 -preset fast -crf 24 -pix_fmt yuv420p10le OUTPUT.mkv
```

crf 取值：x264 约 0-51（默认 23，越小质量越高）；x265 默认 28。

## NVIDIA 硬件加速（NVENC）

```shell
# h264
ffmpeg -i INPUT.mp4 -c:v h264_nvenc -preset fast -cq 23 OUTPUT.mkv
# h265（含 10bit）
ffmpeg -i INPUT.mp4 -c:v hevc_nvenc -pix_fmt p010le -rc vbr -cq:v 27 OUTPUT.mkv
```

## Apple Silicon 硬件加速（VideoToolbox）

```shell
# q:v 1-100，1 最低、100 最高
ffmpeg -i INPUT.mp4 -c:v h264_videotoolbox -q:v 65 OUTPUT.mkv
ffmpeg -i INPUT.mp4 -c:v hevc_videotoolbox -q:v 65 OUTPUT.mkv
```

## 无损封装

```shell
# 仅更换容器，全部流直接拷贝
ffmpeg -i INPUT.mp4 -c copy OUTPUT.mkv
```

## 添加字幕

```shell
ffmpeg -i INPUT -sub_charenc UTF-8 -i INPUT.ass -map 0 -map '-0:s?' -map 1 -map '0:s?' -c copy \
  -metadata:s:s:0 language=chi -metadata:s:s:0 title="简体中文" OUTPUT
```

batcher 组合流程会为每条字幕追加独立输入。默认先映射原视频的非字幕流，再映射新增字幕，最后保留原字幕，新增字幕序号因此从 0 开始且不依赖原字幕数量。

高级参数中的空格需要正确引用，例如 `-metadata title="hello  world"` 会保留标题中的两个空格。输入参数 `-i` 与覆盖选项 `-y` 不允许写入高级参数。

## 添加多个字体

```shell
ffmpeg -i INPUT -map 0 -c copy -attach FONT.ttf \
  -metadata:s:t:m:filename:FONT.ttf mimetype=application/x-truetype-font OUTPUT.mkv
```

字体 MIME 元数据按附件文件名匹配，不依赖原视频已有的附件数量，也不会改写其他附件的 MIME 类型。

## 相关工具

- 视频切割 && 导出字幕推荐 [Lossless Cut](https://github.com/mifi/lossless-cut)。
- 导出字幕文件：`ffmpeg -i input.mkv -map 0:s:0 subs.ass`。
