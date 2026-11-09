# CHANGELOG

## Unreleased

## v0.0.5

- REFACTOR: 项目结构面向 ffmpeg 批量转码精简：拆分 internal/ffmpeg 大文件（option/batcher/execute），
  预设逻辑独立为 internal/presets 包，移除未使用的 internal/utils（含 zap 依赖）
- FEAT: 新增内置转码预设（无损封装、H.264/H.265、NVIDIA/Apple 硬件加速），内置预设只读
- FEAT: 自定义预设改存 CLI 可执行文件所在目录下的 presets/ 文件夹（可用 BATCHER_PRESETS_DIR 覆盖）
- TUI: 转码参数页区分内置/自定义预设，支持从内置一键复制微调；预览页增加加载动画；
  执行页实时显示耗时与失败数；优化参数管理页与结果页展示
- CHORE: 发布流水线修复：GoReleaser 升级到 v2 语法（ldflags 注入路径），TUI 标题栏展示由
  ldflags 注入的版本号；修复 git.tag_sort 非法值导致 Release 误报 "git doesn't contain any tags"；
  精简构建矩阵为 windows/macos/linux × amd64/arm64，386 仅保留 Windows；
  新增 Snapshot workflow 用于无 tag 验证构建产物
- CI: Test workflow 安装 ffmpeg 以支持 ExecuteBatch 真实执行路径测试，push 触发限定 main 分支

## v0.0.5-rc.1

- REFACTOR: 项目结构面向 ffmpeg 批量转码精简：拆分 internal/ffmpeg 大文件（option/batcher/execute），
  预设逻辑独立为 internal/presets 包，移除未使用的 internal/utils（含 zap 依赖）
- FEAT: 新增内置转码预设（无损封装、H.264/H.265、NVIDIA/Apple 硬件加速），内置预设只读
- FEAT: 自定义预设改存 CLI 可执行文件所在目录下的 presets/ 文件夹（可用 BATCHER_PRESETS_DIR 覆盖）
- TUI: 转码参数页区分内置/自定义预设，支持从内置一键复制微调；预览页增加加载动画；
  执行页实时显示耗时与失败数；优化参数管理页与结果页展示
- CHORE: 发布流水线修复：GoReleaser 升级到 v2 语法（tag 排序、ldflags 注入路径、忽略不支持的
  darwin/386 与 darwin/arm 构建）；TUI 标题栏展示由 ldflags 注入的版本号；
  Release workflow 补齐 contents:write 权限并拉取完整提交历史

## v0.0.4

- FIX: fix fonts path error
- REFACTOR: remove advance flag in add fonts cmd
