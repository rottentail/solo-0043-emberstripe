package diffreview

import (
	"encoding/base64"
	"html/template"
	"os"
	"path/filepath"
)

type htmlRegion struct {
	R       Region
	Compare template.URL // data URI when embeddable
}

type htmlView struct {
	Rep        *Report
	Regions    []htmlRegion
	HasRegions bool
}

// RenderHTML renders a single offline Chinese workbench page for the review.
// Comparison clips are embedded as data URIs when readable; native excerpts
// stay as relative links inside the bundle.
func RenderHTML(rep *Report, dir string) ([]byte, error) {
	view := htmlView{Rep: rep, HasRegions: len(rep.Regions) > 0}
	for _, r := range rep.Regions {
		hr := htmlRegion{R: r}
		if r.Files.CompareWAV != "" && dir != "" {
			if b, err := os.ReadFile(filepath.Join(dir, r.Files.CompareWAV)); err == nil {
				hr.Compare = template.URL("data:audio/wav;base64," + base64.StdEncoding.EncodeToString(b))
			}
		}
		view.Regions = append(view.Regions, hr)
	}
	var buf safeBuffer
	if err := reportTemplate.Execute(&buf, view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type safeBuffer struct{ b []byte }

func (s *safeBuffer) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *safeBuffer) Bytes() []byte               { return s.b }

var reportTemplate = template.Must(template.New("diff-review").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>离线差异听审 · Emberstripe</title>
<style>
:root{--ink:#23201b;--mut:#6f675c;--line:#ddd3c2;--paper:#f7f2e7;--card:#fffdf7;--amber:#b45309;--red:#b91c1c;--green:#15803d;--blue:#1d4ed8;}
*{box-sizing:border-box}
body{margin:0;background:var(--paper);color:var(--ink);font:15px/1.65 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif}
header{padding:22px 28px;background:#2b2620;color:#f7f2e7}
header h1{margin:0 0 4px;font-size:21px}
header p{margin:2px 0;color:#cdbf9f;font-size:13px}
main{max-width:1180px;margin:0 auto;padding:22px 28px 60px}
section{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:16px 20px;margin:18px 0}
h2{font-size:17px;margin:4px 0 12px;border-left:4px solid var(--amber);padding-left:10px}
h3{font-size:15px;margin:14px 0 6px}
table{border-collapse:collapse;width:100%;font-size:13px}
th,td{border:1px solid var(--line);padding:6px 9px;vertical-align:top;text-align:left}
th{background:#f0e7d4}
code{background:#f0e7d4;padding:1px 5px;border-radius:4px;font-size:12px;word-break:break-all}
.tag{display:inline-block;padding:1px 9px;border-radius:999px;font-weight:700;font-size:12px;white-space:nowrap}
.t-content_change{background:#fde2e2;color:var(--red);border:1px solid #f3b4b4}
.t-readability_change{background:#fef0d3;color:var(--amber);border:1px solid #f2d199}
.t-unaligned{background:#e3e9fd;color:var(--blue);border:1px solid #b9c8f6}
.t-same{background:#e2f3e8;color:var(--green);border:1px solid #b6e0c4}
.cue{font-size:12px;color:var(--mut)}
.region{border:1px solid var(--line);border-radius:8px;padding:12px 14px;margin:12px 0;background:#fff}
audio{width:100%;margin:6px 0}
.links a{margin-right:14px;font-size:13px;color:var(--blue)}
.small{color:var(--mut);font-size:12px}
.kpi{display:flex;gap:14px;flex-wrap:wrap;margin:8px 0}
.kpi div{background:#f0e7d4;border-radius:8px;padding:8px 14px;font-size:13px}
.kpi b{font-size:18px;display:block}
</style>
</head>
<body>
<header>
  <h1>离线差异听审件 · 两个冻结交接包对照</h1>
  <p>{{.Rep.Schema}} · 生成 {{.Rep.GeneratedAt}} · 离线文件，不访问当前扫描卷</p>
  <p>标记：内容变化 content_change ／ 可读性变化 readability_change（可确认的覆盖变化）／ 无法对位 unaligned（网格不同且证据不足，保留双包复听位置）</p>
</header>
<main>
<section>
<h2>出处：两个原包</h2>
<table>
<tr><th>角色</th><th>包 ID（ZIP SHA-256）</th><th>文件</th><th>卷</th><th>source / audio 摘要</th><th>画格窗</th><th>fps</th><th>每格行数</th><th>采样率</th><th>标定</th><th>校验</th></tr>
{{range .Rep.Sources}}
<tr>
<td><b>{{.Role}}</b></td>
<td><code>{{.ParcelID}}</code></td>
<td class="small">{{.GivenPath}}</td>
<td><code>{{.Capture}}</code></td>
<td class="small">src <code>{{.SourceSHA256}}</code><br>wav <code>{{.AudioSHA256}}</code></td>
<td>[{{.Window.Start}},{{.Window.End}})</td>
<td>{{.FPS}}</td><td>{{.RowsPerFrame}}</td><td>{{.SampleRate}}</td>
<td>{{.Gauge.Profile}}</td><td>{{if .Verified}}✅ 已校验{{else}}⚠️{{end}}</td>
</tr>
{{end}}
</table>
<p class="small">复听：按包 ID 找到 <code>&lt;id&gt;.zip</code>，解压 <code>sound.wav</code>，跳到下表/卡片给出的 PCM 帧或时间码。</p>
</section>

<section>
<h2>时间基与重合窗口</h2>
<div class="kpi">
<div><b>{{.Rep.Timebase.SameFPS}}</b>相同画格率</div>
<div><b>{{.Rep.Timebase.SameGrid}}</b>相同网格（每格行数）</div>
<div><b>{{.Rep.Summary.OverlapFrames}}</b>重合画格</div>
<div><b>{{.Rep.Summary.Regions}}</b>听审区域</div>
<div><b>{{.Rep.Summary.CoverageChanges}}</b>覆盖变化段</div>
<div><b>{{.Rep.Summary.AcousticChanges}}</b>声学变化段</div>
<div><b>{{.Rep.Summary.UnalignedIntervals}}</b>无法对位段</div>
</div>
<p class="small">对位方式 <code>{{.Rep.Timebase.AlignmentBasis}}</code>；共同计量率 {{.Rep.Timebase.ComparisonRate}} Hz。
{{if .Rep.Overlap}}重合半开窗 <b>[{{.Rep.Overlap.Start}},{{.Rep.Overlap.End}})</b>。{{else}}<b>无重合画格。</b>{{end}}</p>
{{range .Rep.Notes}}<p class="small">⚠️ {{.}}</p>{{end}}
</section>

<section>
<h2>同一画格位置的标记</h2>
<table>
<tr><th>画格</th><th>标记</th><th>网格</th><th>覆盖证据</th><th>声学证据</th><th>A 复听位置</th><th>B 复听位置</th><th>区域</th></tr>
{{range .Rep.Frames}}
<tr>
<td><b>{{.Frame}}</b></td>
<td><span class="tag t-{{.Mark}}">{{.MarkZh}}</span><br><code>{{.Mark}}</code></td>
<td>{{if .GridAligned}}同格{{else}}异网格{{end}}</td>
<td class="small">
{{range .CoverageChanges}}<div>{{.StatusAZh}}→{{.StatusBZh}} @{{printf "%.3f" .Span.FrameStart}}–{{printf "%.3f" .Span.FrameEnd}}；可听状态改变={{.AudibleStateChanged}}</div>{{else}}—{{end}}
</td>
<td class="small">
{{range .Acoustic}}<div>{{.Basis}} n={{.ComparedFrames}} rmsΔ=[{{printf "%.4f" (index .RMSDeltaNorm 0)}},{{printf "%.4f" (index .RMSDeltaNorm 1)}}] → <code>{{.Conclusion}}</code></div>{{end}}
{{range .Unaligned}}<div>无法对位：<code>{{.Reason}}</code></div>{{end}}
{{if and (not .Acoustic) (not .Unaligned)}}—{{end}}
</td>
<td class="cue">格{{.Cues.A.Frame}} 行{{.Cues.A.RowInFrame}}<br>{{.Cues.A.WAVTimecode}} / #{{.Cues.A.PCMFrame}}</td>
<td class="cue">格{{.Cues.B.Frame}} 行{{.Cues.B.RowInFrame}}<br>{{.Cues.B.WAVTimecode}} / #{{.Cues.B.PCMFrame}}</td>
<td>{{.RegionID}}</td>
</tr>
{{end}}
</table>
</section>

{{if .HasRegions}}
<section>
<h2>抽取的听审区域</h2>
<p class="small">每个区域提供 A→间隔→B 对照音频（已内嵌），以及两个原包各自采样率的原速摘录文件；整片连播见 <code>review.wav</code>。</p>
{{range .Regions}}
{{$r := .R}}
<div class="region">
<h3>{{$r.ID}} · <span class="tag t-{{$r.Mark}}">{{$r.MarkZh}}</span> <span class="small">画格 {{range $i,$f := $r.Frames}}{{if $i}}, {{end}}{{$f}}{{end}} · {{printf "%.3f" $r.Span.FrameStart}}–{{printf "%.3f" $r.Span.FrameEnd}} 格</span></h3>
<p class="small">{{$r.ListeningHintZh}}</p>
{{if .Compare}}<audio controls preload="none" src="{{.Compare}}"></audio>{{else}}<audio controls preload="none" src="{{$r.Files.CompareWAV}}"></audio>{{end}}
{{range $r.CoverageEvidence}}<p class="small">覆盖证据：<code>{{.StatusA}}</code>({{.StatusAZh}}) → <code>{{.StatusB}}</code>({{.StatusBZh}})，可听状态改变={{.AudibleStateChanged}}；{{.Confirmation}}</p>{{end}}
{{range $r.AcousticEvidence}}<p class="small">声学证据 [{{.Basis}}] compared={{.ComparedFrames}}，RMSΔnorm=[{{printf "%.4f" (index .RMSDeltaNorm 0)}},{{printf "%.4f" (index .RMSDeltaNorm 1)}}]，PeakΔnorm=[{{printf "%.3f" (index .PeakDeltaNorm 0)}},{{printf "%.3f" (index .PeakDeltaNorm 1)}}]，结论 <code>{{.Conclusion}}</code></p>{{end}}
{{range $r.Unaligned}}<p class="small">无法对位：{{.Detail}}（<code>{{.Reason}}</code>）</p>{{end}}
<div class="links">
<a href="{{$r.Files.CompareWAV}}">对照 WAV</a>
<a href="{{$r.Files.AWAV}}">A 原速摘录</a>
<a href="{{$r.Files.BWAV}}">B 原速摘录</a>
</div>
<p class="cue">A cue：格 {{$r.Cues.A.Frame}} 行 {{$r.Cues.A.RowInFrame}} / sound.wav #{{$r.Cues.A.PCMFrame}} / {{$r.Cues.A.WAVTimecode}} / 字节 {{$r.Cues.A.WAVByteOffset}} / 包 <code>{{$r.Cues.A.ParcelID}}</code></p>
<p class="cue">B cue：格 {{$r.Cues.B.Frame}} 行 {{$r.Cues.B.RowInFrame}} / sound.wav #{{$r.Cues.B.PCMFrame}} / {{$r.Cues.B.WAVTimecode}} / 字节 {{$r.Cues.B.WAVByteOffset}} / 包 <code>{{$r.Cues.B.ParcelID}}</code></p>
</div>
{{end}}
</section>
{{end}}

{{if .Rep.Exclusive}}
<section>
<h2>窗口独占区间（不能同格对照）</h2>
<table><tr><th>侧</th><th>画格区间</th><th>复听 cue</th></tr>
{{range .Rep.Exclusive}}<tr><td>{{.Side}}</td><td>[{{.FrameStart}},{{.FrameEnd}})</td><td class="cue">格{{.Cue.Frame}} 行{{.Cue.RowInFrame}} / #{{.Cue.PCMFrame}} / {{.Cue.WAVTimecode}} / 包 <code>{{.Cue.ParcelID}}</code></td></tr>{{end}}
</table>
</section>
{{end}}

<section>
<h2>判定策略</h2>
<p class="small">{{.Rep.Policy.NoteZh}}</p>
<p class="small">主标记优先级：<code>{{.Rep.Policy.FrameMarkPrecedence}}</code></p>
</section>
</main>
</body>
</html>`))
