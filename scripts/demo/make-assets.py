#!/usr/bin/env python3
"""Build onesie, replay the recorded answers through it and render demo.gif and social-preview.png.

Usage: python3 scripts/demo/make-assets.py
It writes .github/assets/demo.gif and .github/assets/social-preview.png and .svg, and keeps its
build, run and frame files in a temporary directory. data/record.sh records the answers it replays.

Each shown command is the exact shell text run by /bin/sh in a clean environment, where `onesie`
is a shell function calling the freshly built binary. ONESIE_MOCK is set in that environment and
never shown. Scenes 1 and 2 get `-o table` added at run time only, since a terminal shows the
table view by default and the run here has no terminal.
"""

import json
import shutil
import tempfile
import subprocess
import sys
from pathlib import Path
from xml.sax.saxutils import escape

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
DATA = HERE / "data"
ASSETS = REPO / ".github" / "assets"
OUT = Path(tempfile.mkdtemp(prefix="onesie-demo-"))
BIN = OUT / "onesie"
RUN = OUT / "run"
RSVG = shutil.which("rsvg-convert") or "/opt/homebrew/bin/rsvg-convert"
FFMPEG = shutil.which("ffmpeg") or "/opt/homebrew/bin/ffmpeg"

# palette, shared by the GIF and the social preview
BG = "#0f141a"
FG = "#e6edf3"
DIM = "#7d8590"
PROMPT = "#3fb950"
ACCENT = "#79c0ff"
BAR = "#e9ebee"
BAR_EDGE = "#c9ced4"
DOTS = ("#ff5f57", "#febc2e", "#28c840")
MONO = "Menlo, Monaco, 'DejaVu Sans Mono', monospace"
SANS = "'Helvetica Neue', Helvetica, Arial, sans-serif"

# terminal geometry for the GIF
COLS = 100
MAX_ROWS = 35
FONT = 14.4
CW = FONT * 0.6021  # Menlo advance width
LH = 19
PAD = 16
BARH = 30
FPS = 12
CHARS_PER_STEP = 6
FRAMES_PER_STEP = 2  # 36 characters a second at 12 fps

URGENT_ON = "echo 'checkout is down for everyone, customers cannot pay' |"
URGENT_OFF = "echo 'please update the billing address on my invoice' |"
GATE = "  onesie -f urgent.yaml -q && echo 'paging on-call'"


def main():
    build()
    prepare_run_dir()
    screens = run_scenes()
    make_gif(screens)
    make_social(screens[1])
    print("commands shown:")
    for screen in screens:
        for item in screen:
            if item["kind"] == "step":
                print("  $ " + "\n    ".join(item["lines"]), f"  [exit {item['code']}]")


def build():
    subprocess.run(["go", "build", "-o", str(BIN), "./cmd/onesie"], cwd=REPO, check=True)


def prepare_run_dir():
    if RUN.exists():
        shutil.rmtree(RUN)
    RUN.mkdir()
    for name in ("urgent.yaml", "tickets.jsonl"):
        shutil.copy(DATA / name, RUN / name)


def run_scenes():
    calm = recorded_value(DATA / "single-calm.json", "urgent")
    if calm != 0.21:
        sys.exit(f"single-calm.json answered {calm}, the comment says 0.21")

    simple = [
        step(["echo 'the checkout page is down for everyone' | onesie 'is this urgent'"],
             mock="simple-yes-no.json", add_table=True),
        step(["echo 'I was charged twice for my order' |",
              "  onesie 'who should handle this' --pick billing,shipping,technical"],
             mock="simple-pick.json", add_table=True),
    ]

    ask = step(["onesie --ask urgent='does this ticket need someone within the hour' \\",
                "  --print-questions > urgent.yaml"], expect_silent=True)
    printed = (RUN / "urgent.yaml").read_text()
    if printed != (DATA / "urgent.yaml").read_text():
        sys.exit("--print-questions no longer matches the recorded urgent.yaml")
    dropped = drop_wide_lines(RUN / "urgent.yaml")
    gate = [
        ask,
        step(["cat urgent.yaml"]),
        blank(),
        step([URGENT_ON, "  onesie -f urgent.yaml -o raw"], mock="single-urgent.json"),
        blank(),
        step([URGENT_OFF, GATE], mock="single-calm.json", expect_code=1, expect_silent=True),
        comment(f"# {calm}, not urgent, nobody paged"),
        blank(),
        step([URGENT_ON, GATE], mock="single-urgent.json"),
    ]
    if gate[3]["output"] != ["0.93"]:
        sys.exit(f"-o raw printed {gate[3]['output']}, expected 0.93")
    if gate[-1]["output"] != ["paging on-call"]:
        sys.exit(f"the urgent gate printed {gate[-1]['output']}")

    calibrate_lines = ["onesie calibrate -f urgent.yaml -i jsonl --map .text --label .urgent \\",
                       "  --cuts 0.3,0.5,0.7,0.9 < tickets.jsonl"]
    calibrate = step(calibrate_lines, mock="answers.jsonl")
    check_report(calibrate_lines, calibrate)
    calib = [step(["head -2 tickets.jsonl"]), blank(), calibrate]

    for d in dropped:
        print(f"dropped from the shown urgent.yaml: {d}")
    return [with_blank_between(simple), gate, calib]


def recorded_value(path, question):
    return json.loads(path.read_text())[question]["value"]


def drop_wide_lines(path):
    kept, dropped = [], []
    for line in path.read_text().splitlines():
        (dropped if len(line) > COLS else kept).append(line)
    path.write_text("\n".join(kept) + "\n")
    return dropped


def step(lines, mock=None, add_table=False, expect_code=0, expect_silent=False):
    script = "\n".join(lines)
    run = script.replace("onesie 'is this urgent'", "onesie 'is this urgent' -o table") \
        .replace("--pick billing,shipping,technical", "--pick billing,shipping,technical -o table") \
        if add_table else script
    if add_table and run.count("-o table") != 1:
        sys.exit(f"could not add -o table to {script!r}")
    code, text = sh(run, mock)
    if code != expect_code:
        sys.exit(f"{script!r} exited {code}, expected {expect_code}:\n{text}")
    output = text.rstrip("\n").splitlines() if text.strip() else []
    if expect_silent and output:
        sys.exit(f"{script!r} printed {output}, expected nothing")
    return {"kind": "step", "lines": lines, "output": output, "code": code}


def sh(script, mock=None):
    env = {
        "PATH": "/usr/bin:/bin",
        "HOME": str(OUT / "home"),
        "ONESIE_CONFIG_DIR": str(OUT / "cfg"),
        "XDG_CACHE_HOME": str(OUT / "cache"),
    }
    for d in ("home", "cfg", "cache"):
        (OUT / d).mkdir(exist_ok=True)
    if mock:
        env["ONESIE_MOCK"] = str(DATA / mock)
    wrapped = f'onesie() {{ "{BIN}" "$@"; }}\n{script}\n'
    p = subprocess.run(["/usr/bin/env", "-i", *[f"{k}={v}" for k, v in env.items()],
                        "/bin/sh", "-c", wrapped],
                       cwd=RUN, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                       stderr=subprocess.STDOUT, text=True)
    return p.returncode, p.stdout


def check_report(lines, shown):
    script = "\n".join(lines) + " 2>/dev/null"
    _, stdout_only = sh(script, "answers.jsonl")
    want = (DATA / "report.txt").read_text()
    if stdout_only != want:
        sys.exit("the calibrate report differs from report.txt:\n" + stdout_only)
    report = [l for l in shown["output"] if not l.startswith("onesie: ")]
    if report != want.rstrip("\n").splitlines():
        sys.exit("the shown calibrate output differs from report.txt")


def with_blank_between(steps):
    out = []
    for i, s in enumerate(steps):
        if i:
            out.append(blank())
        out.append(s)
    return out


def blank():
    return {"kind": "blank"}


def comment(text):
    return {"kind": "comment", "text": text}


# ---------- GIF ----------

def make_gif(screens):
    frames = []  # (rows, cursor (row, col) or None, frame count)
    for i, screen in enumerate(screens):
        last = i == len(screens) - 1
        frames += screen_frames(screen, hold=4.0 if last else 2.5)

    tallest = max(len(f[0]) for f in frames)
    if tallest > MAX_ROWS:
        sys.exit(f"a screen is {tallest} rows, taller than {MAX_ROWS}")
    widest = max(sum(len(t) for t, _ in r) for f in frames for r in f[0])
    if widest > COLS:
        sys.exit(f"a line is {widest} columns, wider than {COLS}")
    width = round(COLS * CW + 2 * PAD)
    height = round(BARH + PAD + tallest * LH + PAD + 6)

    fdir = OUT / "frames"
    if fdir.exists():
        shutil.rmtree(fdir)
    fdir.mkdir()
    concat = []
    for i, (frows, cursor, count) in enumerate(frames):
        svg = fdir / f"f{i:04d}.svg"
        png = fdir / f"f{i:04d}.png"
        svg.write_text(terminal_svg(width, height, frows, cursor))
        subprocess.run([RSVG, "-o", str(png), str(svg)], check=True)
        concat.append(f"file '{png.name}'\nduration {count / FPS:.4f}")
    concat.append(f"file 'f{len(frames) - 1:04d}.png'")
    (fdir / "list.txt").write_text("\n".join(concat) + "\n")

    gif = ASSETS / "demo.gif"
    subprocess.run([
        FFMPEG, "-y", "-loglevel", "error", "-f", "concat", "-safe", "0",
        "-i", str(fdir / "list.txt"),
        "-vf", "split[a][b];[a]palettegen=max_colors=48:stats_mode=full[p];"
               "[b][p]paletteuse=dither=none:diff_mode=rectangle",
        "-fps_mode", "vfr", "-loop", "0", str(gif),
    ], check=True)
    print(f"demo.gif: {gif.stat().st_size} bytes, {width}x{height}, "
          f"{sum(f[2] for f in frames) / FPS:.1f}s, tallest screen {tallest} rows")


def screen_frames(screen, hold):
    frames = []
    rows = []
    for item in screen:
        if item["kind"] == "blank":
            rows = rows + [[]]
        elif item["kind"] == "comment":
            rows = rows + [[("$ ", PROMPT), (item["text"], DIM)]]
            frames.append((rows, None, 8))
        else:
            frames += typing(rows, item["lines"])
            rows = rows + command_rows(item["lines"], None)
            frames.append((rows, None, 5))
            rows = rows + [output_row(l) for l in item["output"]]
            frames.append((rows, None, 8 if item["output"] else 6))
    frames.append((rows + [prompt_row("")], (len(rows), 2), int(FPS * hold)))
    return frames


def output_row(line):
    if line.startswith("onesie: "):
        return [(line, DIM)]
    if line.lstrip().startswith("cut ") or line == "worst misses":
        return [(line, ACCENT)]
    return [(line, FG)]


def prompt_row(text):
    return [("$ ", PROMPT), (text, FG)]


def typing(base, lines):
    total = sum(len(l) for l in lines)
    out = []
    for n in list(range(0, total, CHARS_PER_STEP)) + [total]:
        rows = command_rows(lines, n)
        col = sum(len(t) for t, _ in rows[-1])
        out.append((base + rows, (len(base) + len(rows) - 1, col), FRAMES_PER_STEP))
    return out


def command_rows(lines, upto):
    """Rows for a command of one or more lines, typed up to `upto` characters."""
    rows = []
    remaining = upto
    for i, line in enumerate(lines):
        part = line
        if remaining is not None:
            if i > 0 and remaining <= 0:
                break
            part = line[:max(0, remaining)]
            remaining -= len(line)
        rows.append([("$ " if i == 0 else "", PROMPT), (part, FG)])
    return rows


def terminal_svg(width, height, rows, cursor):
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
        f'viewBox="0 0 {width} {height}">',
        f'<rect width="{width}" height="{height}" rx="10" fill="{BG}"/>',
        f'<path d="M0 10a10 10 0 0 1 10-10h{width - 20}a10 10 0 0 1 10 10v{BARH - 10}H0z" fill="{BAR}"/>',
        f'<rect y="{BARH - 1}" width="{width}" height="1" fill="{BAR_EDGE}"/>',
    ]
    for i, c in enumerate(DOTS):
        parts.append(f'<circle cx="{20 + i * 20}" cy="{BARH / 2}" r="6" fill="{c}"/>')
    parts.append(f'<text x="{width / 2}" y="{BARH / 2 + 4.5}" text-anchor="middle" '
                 f'font-family="{SANS}" font-size="13" fill="#5b636b">onesie</text>')
    top = BARH + PAD
    for r, row in enumerate(rows):
        y = top + r * LH + FONT
        col = 0
        for text, color in row:
            if text:
                parts.append(text_el(PAD + col * CW, y, text, color, FONT))
            col += len(text)
    if cursor:
        r, c = cursor
        parts.append(f'<rect x="{PAD + c * CW:.2f}" y="{top + r * LH + 2}" width="{CW:.2f}" '
                     f'height="{LH - 3}" fill="{FG}" opacity="0.85"/>')
    parts.append("</svg>")
    return "\n".join(parts)


def text_el(x, y, text, color, size, family=MONO, weight="normal"):
    return (f'<text x="{x:.2f}" y="{y:.2f}" xml:space="preserve" font-family="{family}" '
            f'font-size="{size}" font-weight="{weight}" fill="{color}">{escape(text)}</text>')


# ---------- social preview ----------

def make_social(gate_screen):
    W, H, M = 1280, 640, 40
    tagline = ["Gate scripts and CI on fast, calibrated System One",
               "judgments: a score, a choice or an exit code."]
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}">',
        f'<rect width="{W}" height="{H}" fill="#080b0f"/>',
        text_el(M + 40, 140, "onesie", FG, 96, MONO, "bold"),
    ]
    for i, line in enumerate(tagline):
        parts.append(text_el(M + 44, 200 + i * 42, line, "#b6c2cf", 32, SANS))

    pair = gate_screen[gate_screen.index(next(
        s for s in gate_screen if s["kind"] == "step" and s["lines"][0] == URGENT_OFF)):]
    rows = []
    for item in pair:
        if item["kind"] == "blank":
            continue
        if item["kind"] == "comment":
            rows.append([("$ ", PROMPT), (item["text"], DIM)])
            continue
        rows += command_rows(item["lines"], None)
        rows += [output_row(l) for l in item["output"]]

    size = 21
    cw = size * 0.6021
    lh = 31
    px, py, pw = M + 40, 300, W - 2 * (M + 40)
    ph = BARH + 18 + len(rows) * lh + 8
    if py + ph > H - M:
        sys.exit(f"social preview panel ends at {py + ph}, past the safe margin {H - M}")
    parts += [
        f'<rect x="{px}" y="{py}" width="{pw}" height="{ph}" rx="10" fill="{BG}" '
        f'stroke="#2a313a"/>',
        f'<path d="M{px} {py + 10}a10 10 0 0 1 10-10h{pw - 20}a10 10 0 0 1 10 10v{BARH - 10}'
        f'H{px}z" fill="{BAR}"/>',
    ]
    for i, c in enumerate(DOTS):
        parts.append(f'<circle cx="{px + 20 + i * 20}" cy="{py + BARH / 2}" r="6" fill="{c}"/>')
    for r, row in enumerate(rows):
        y = py + BARH + 18 + r * lh + size * 0.8
        col = 0
        for text, color in row:
            if text:
                parts.append(text_el(px + 22 + col * cw, y, text, color, size))
            col += len(text)
        if 22 + col * cw > pw - 10:
            sys.exit("social preview line is too wide for the panel")
    parts.append("</svg>")
    svg = ASSETS / "social-preview.svg"
    svg.write_text("\n".join(parts))
    png = ASSETS / "social-preview.png"
    subprocess.run([RSVG, "-w", str(W), "-h", str(H), "-o", str(png), str(svg)], check=True)
    print(f"social-preview.png: {png.stat().st_size} bytes, panel ends at {py + ph}")


if __name__ == "__main__":
    main()
