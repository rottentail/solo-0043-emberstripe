# Emberstripe 胶片光学声轨修复工位

Emberstripe 从胶片扫描工位交来的灰度 PNG 条带中读取双声道可变面积光学声轨。操作员可以核对灰阶标定、追踪任意画格对应的声槽、试听 PCM，并把音频和采样位置索引交给下一个修复工位。服务处理本机受信目录中的卷数据。

## 启动

需要 Go 1.23 或以上。服务和样例生成器使用 Go 标准库。复走脚本使用 Python 3.9 或以上的标准库。

```sh
go build ./...
go run ./cmd/samples -out var/scans
go run ./cmd/emberstripe -addr 127.0.0.1:7431 -scans var/scans -parcels var/parcels
```

样例生成器要求目标卷目录尚不存在。`cinder-a` 是 24 格/秒、每格 2000 行、声槽领先 230 行的右移取样案例；`vireo-b` 是 25 格/秒、每格 1920 行、偏移 −135 行的案例。两卷原生 PCM 采样率均为 48 kHz。两卷均含一张缺页和一张 8 位灰阶条带，其余条带是 16 位灰阶。`bench` 和 `soft` 是对比度不同的有效标定，`flat` 的参考窗口缺少对比度，`absent` 的暗场参考页缺失。

## 浏览器操作

启动后打开 `http://127.0.0.1:7431`。选择扫描卷、画格区间和光学标定，然后应用区间。点击扫描条带、波形或覆盖区间，让画格探针定位到同一行；也可以直接输入画格和行号。画面缺页标记在画格带上，偏移后的声槽缺口显示在波形中。

选择另一个可用标定后点击「比较」，可看到共同可读位置的左右声道差值。「载入试听」获取应用窗口的 WAV，试听控件与探针共享原生采样时钟。「冻结交接包」生成含 WAV 和画格索引的 ZIP，支持下载和再次校验。工作台静态资源随 Go 程序内嵌，无需前端构建或在线资源。

## 从画格检查到交付

在另一个终端运行以下命令。

```sh
base=http://127.0.0.1:7431
curl "$base/captures"
curl "$base/captures/cinder-a/header"
curl "$base/captures/cinder-a/calibration?profile=bench"
curl "$base/captures/cinder-a/register?frame=1&row=1770"
curl "$base/captures/cinder-a/coverage?start=1&end=4"
curl "$base/captures/cinder-a/trace?frame=0&row=400"
curl "$base/captures/cinder-a/compare?start=1&end=4&profile=bench&against=soft"
curl "$base/captures/cinder-a/overlay?frame=2&profile=bench" -o var/check.png
curl "$base/captures/cinder-a/audition?start=1&end=4&profile=soft" -o var/listen.wav
curl -X POST "$base/captures/cinder-a/release?start=1&end=4&profile=soft"
```

发布返回的 `Location` 可下载 ZIP，追加 `/verify` 得到完整性、时钟和覆盖检查结果。包内 `sound.wav` 是交错 16 位立体声，`register.json` 包含画格到包内 PCM 帧的索引，以及以卷内画格行为单位的实际声槽覆盖、标定值和字节摘要。索引里的画面摘要取自登记的扫描页，原图可向扫描工位调取。相同卷、画格窗和标定重复发布会得到同一标识。

`start` 包含起点、`end` 排除终点，二者都是零基画格号。`frame`、`row` 也是零基。以 `cinder-a` 为例，画格 1 的第 1770 行使用第 2 页的第 0 行声槽。第 2 页未扫描，试听 WAV 在相应位置保留双声道零值。区间有音频零值时，以 `coverage` 区分缺页、越出卷界和可读样本。`overlay` 左侧是该画格的画面灰度条带，右侧两个带分别表示映射到同一画格行的左／右声音。亮橙表示样本位置，亮红表示超出标定范围，暗红底表示该带没有输入材料。

输入格式、具体坐标规则和局部资源上限见 `PROJECT_SPEC.md`。真实工作流复走如下。默认运行时用临时目录及临时回环端口。

```sh
python3 examples/walkthrough.py all
python3 examples/walkthrough.py release
```

## 离线差异听审件（两个交接包对照）

剪辑台拿到的是两次交接各自的冻结 ZIP（都可用 `GET /parcels/{id}/verify` 或 `parcel.Verify` 校验）。`cmd/diffreview` 是完全离线的对照工具：不启动服务、不读取当前扫描卷，只校验两个原包并产出“差异听审件”——逐画格标记 **内容变化 `content_change`**、**可读性变化 `readability_change`**（可确认的覆盖变化）或 **无法对位 `unaligned`**，并保留两个原包的出处与可复听 cue。

```sh
# 用内置夹具一次生成两个“同 fps、不同每格行数、窗口部分重合”的可校验包并出件
go run ./cmd/diffreview demo -out var/diff-demo
python3 examples/diff_review.py demo -out var/diff-demo   # 等价启动器

# 对照任意两个已校验交接包（ZIP 路径）
go run ./cmd/diffreview -a handoff-a.zip -b handoff-b.zip -out var/diff-review

# 或按归档 store 中的包 ID（<parcels>/<id>.zip，额外校验 ID=整包 SHA-256）
go run ./cmd/diffreview \
  -a-id 051a2f16…35106 -b-id 144ee890…42165 \
  -parcels var/parcels -out var/diff-review
```

产物：

| 文件 | 内容 |
| --- | --- |
| `diff-review.json` | 机器报告：两个原包出处（包 ID/整包 SHA-256、`source_sha256`、`audio_sha256`、卷名、窗口、fps、每格行数、标定、字节数、校验状态）、时间基、重合窗、逐画格标记、覆盖/声学两类分开的证据、双包 cue、独占区间、统计 |
| `diff-review.md` | 中文剪辑台报告（出处表、逐格标记表、证据数值、cue 时间码） |
| `diff-review.html` | 离线听审页，对照音频内嵌，浏览器直接打开 |
| `review.wav` | 全部差异区域连播节目（每区 A→间隔→B，共同采样率） |
| `clips/region-NN-<mark>-{a,b,compare}.wav` | 每区 A/B **原采样率**摘录与共同采样率对照片段 |
| `diff-review.zip`(+`.sha256`) | 上述文件的打包听审件 |

判定规则（阈值可用 flag 调整，见 `-h`）：

- **时间坐标**：以“绝对画格时间”为公共轴。第 `f` 格对应 `[f/fps,(f+1)/fps)`；包内 PCM 帧 `i` 对应绝对画格行 `window.start*rows+i`。窗口只取整数画格交集 `[max(start), min(end))`，不重合部分单列为 `exclusive`（只保留存在侧 cue）。
- **覆盖变化与声学变化分开**：两包 `register.json` 的覆盖分区投影到连续画格时间后逐段比对。状态不同即 **可确认的覆盖变化**（可读↔缺页/卷外时 `audible_state_changed=true`）；不可读侧的补零只作覆盖占位，**不**充当声学差异。只有两侧共同可读的区间才做声学计量。
- **同网格**（fps 与每格行数都相同）：PCM 帧逐样本精确比较，给出 `changed_samples`、`peak_delta`、`rms_delta`；任何非零差异即“内容变化”。
- **异网格**（fps 相同、每格行数不同 → 采样率不同）：按连续时间线性重采样到共同率后计量 RMS/峰值差。差异超过 `--change-rms/--change-peak` 为 **有证据的内容变化**；低于 `--same-rms/--same-peak` 且共同可读跨度足够（`--min-span`）判为一致；介于两者之间、或共同可读跨度太短，则判 **无法对位**，不强行下内容结论。
- **双包可复听位置**：每个画格标记、区域、覆盖/声学证据和独占区间都带 A/B（或单侧）cue：原包 ID、`sound.wav` 内 PCM 帧、WAV 秒点/时间码、字节偏移以及画格/行坐标，可直接回原包复听。
- 画格主标记优先级 `content_change > readability_change > unaligned > same`；同一画格内的子画格分段与混合证据在 JSON/HTML/MD 中分行保留，不会被整格掩盖。
- 包校验失败、参数缺失为退出码 2；发现差异是正常结果，退出码仍为 0，摘要 JSON 打印到 stdout。

单元测试在 `internal/diffreview/diffreview_test.go`，覆盖三种标记、子画格覆盖边界、同网格/异网格计量、部分重合与完全不交窗口、篡改包拒绝、ID store 解析和端到端出件。

独立单元与浏览器回归测试套件延后至后续任务。上面的复走检查使用同一个 Go 服务，通过 HTTP 观察页面、WAV、PNG、JSON、ZIP 和失败状态。
