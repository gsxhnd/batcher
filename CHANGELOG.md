# CHANGELOG

## Unreleased

- REFACTOR: 项目结构面向 ffmpeg 批量转码精简：拆分 internal/ffmpeg 大文件（option/batcher/execute），
  预设逻辑独立为 internal/presets 包，移除未使用的 internal/utils（含 zap 依赖）
- FEAT: 新增内置转码预设（无损封装、H.264/H.265、NVIDIA/Apple 硬件加速），内置预设只读
- FEAT: 自定义预设改存 CLI 可执行文件所在目录下的 presets/ 文件夹（可用 BATCHER_PRESETS_DIR 覆盖）
- TUI: 转码参数页区分内置/自定义预设，支持从内置一键复制微调；预览页增加加载动画；
  执行页实时显示耗时与失败数；优化参数管理页与结果页展示

## v0.0.4

- FIX: fix fonts path error
- REFACTOR: remove advance flag in add fonts cmd
