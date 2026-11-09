# CHANGELOG

## v0.0.7

- TUI: 界面重构为 lazygit 式常驻布局：去掉"操作→转码→字幕→参数→预览→执行"的向导式逐步跳转，
  改为左侧 [1]操作/[2]转码预设/[3]字幕轨道/[4]参数/[5]任务 五个面板 + 右侧焦点内容区，
  数字键 1-5 或 Tab 切换，未勾选对应操作的面板自动变灰跳过；未聚焦面板自动折叠为 2 行、
  聚焦面板展开占满剩余空间，左侧列表（预设/字幕/字段/文件）超出高度时随光标滚动
- TUI: 新增 ? 快捷键帮助弹窗：任意面板按 ? 居中弹出完整按键说明，↑/↓ 可滚动查阅，esc 关闭；
  底栏常驻提示
- TUI: 命令预览实时化：任何配置变化自动防抖重新生成命令列表（任务面板直接查看），
  刻意的"预览"页面移除；执行完成后配置保持不变，可改完直接按 x 重跑
- TUI: 预设/字幕的新增、编辑、删除确认改为居中弹窗；删除不再依赖"二次按 d"；
  面板 [4] 参数表单改为在右侧内容区直接编辑（enter 进入，esc 退出，编辑时全局快捷键失效）
- TUI: 执行期间锁定配置，任务面板内查看进度/日志，q 取消执行（不再强退整个程序）；
  窄终端（< 90 列）自动退化为单面板布局
- FEAT: 字幕轨道支持标记默认字幕流（mkv 输出 -disposition default）：字幕弹窗新增
  「默认字幕流」勾选（space 切换）、面板 [3] 按 t 快捷切换；其余字幕流显式置 0 避免多重
  默认轨道，全部未标记时命令保持与旧版一致
- TUI: 执行时主体内容最上方固定显示当前任务的完整 ffmpeg 命令（高亮、长命令自动折行，
  不随日志滚动截断）
- TUI: 执行日志支持 pgup/pgdn/home/end 与鼠标滚轮滚动；向上滚动暂停跟随最新输出，
  回到底部（end/滚轮）或按 f 自动恢复贴底跟随
- FEAT: 任务面板按 y 复制当前查看/执行的完整命令到系统剪贴板（OSC52，由终端转存）
- FIX: 修复面板化重构后命令预览/执行日志视口丢失高度，导致内容显示为空的回归问题

## v0.0.6

- FEAT: 自定义转码参数预设支持名称：预设存为「名称 + 参数」，列表与转码参数页均以名称为主标签、
  参数为说明（无名称时直接显示参数）；旧版纯字符串预设文件自动兼容读取，不丢数据
- TUI: 预设编辑表单拆为「预设名称 / 高级参数」两字段（Tab 或 ↑/↓ 切换，最后一项 Enter 保存）；
  预设管理页光标覆盖内置+自定义全部条目，选中内置预设可一键复制为自定义预设，
  内置条目按 e/d 提示只读，删除与排序仍仅作用于自定义预设
- REFACTOR: 代码结构优化：internal/ffmpeg 移除共享累积的 cmdBatches 状态、合并目录遍历与
  字幕参数生成中的重复逻辑（字幕流序号统一分配）；internal/tui 统一事件日志写入、
  抽取光标/样式/焦点辅助函数、清理死代码与无用返回值
- CHORE: Test workflow 支持 master 分支推送触发

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
