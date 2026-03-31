#!/usr/bin/env python3
"""Generate an animated GIF showing the triage architecture assembling step-by-step.

Draws boxes and arrows directly with Pillow — no external dependencies beyond Pillow.
Each frame adds new elements in execution order, highlighted in orange.
"""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

OUTPUT_GIF = Path(__file__).parent / "architecture-animated.gif"

# Canvas
W, H = 1250, 850
BG = (255, 255, 255)
PAD = 16  # text padding inside boxes

# Colours
COL_ACTIVE = (74, 144, 217)  # blue — established elements
COL_NEW = (245, 166, 35)  # orange — newly appearing
COL_SANDBOX = (200, 230, 200)  # light green — sandbox background
COL_SANDBOX_BORDER = (92, 184, 92)
COL_EXTERNAL = (220, 220, 220)  # grey — external services
COL_TEXT = (50, 50, 50)
COL_WHITE = (255, 255, 255)
COL_ARROW = (100, 100, 100)
COL_ARROW_NEW = (220, 140, 20)
COL_ARROW_DATA = (74, 144, 217)

# Try to load a nice font, fall back to default
try:
    FONT = ImageFont.truetype("/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf", 13)
    FONT_BOLD = ImageFont.truetype("/usr/share/fonts/dejavu-sans-fonts/DejaVuSans-Bold.ttf", 13)
    FONT_SMALL = ImageFont.truetype("/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf", 11)
    FONT_TITLE = ImageFont.truetype("/usr/share/fonts/dejavu-sans-fonts/DejaVuSans-Bold.ttf", 15)
except OSError:
    try:
        FONT = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 13)
        FONT_BOLD = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", 13)
        FONT_SMALL = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 11)
        FONT_TITLE = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", 15)
    except OSError:
        FONT = ImageFont.load_default()
        FONT_BOLD = FONT
        FONT_SMALL = FONT
        FONT_TITLE = FONT


# ── Box positions (x, y, w, h) ──────────────────────────────────────
# Row 0: title area (y=10..40)
# Row 1: Launcher
# Row 2: GH REST Server, Agent Runner
# Row 3: GitHub API, OpenShell Gateway
# Row 4: Triage sandbox
# Row 5: Subagent sandboxes

BOXES = {
    "launcher": (480, 50, 220, 45),
    "gh_server": (50, 180, 250, 45),
    "agent_runner": (760, 180, 270, 45),
    "github_api": (50, 320, 130, 35),
    "gateway": (810, 320, 190, 35),
    # Triage on the far left — keeps agent_runner→subagent arrows clear
    "triage": (45, 470, 250, 50),
    # Subagents spread across center-right
    "dup": (380, 580, 210, 50),
    "comp": (640, 580, 230, 50),
    "repro": (930, 580, 230, 50),
    # External nodes
    "ext_urls": (670, 710, 130, 35),
    "local_fs": (960, 710, 140, 35),
}

# Sandbox outlines: (x, y, w, h, label)
SANDBOXES = {
    "sb_triage": (30, 440, 280, 100, "triage-write.yaml"),
    "sb_dup": (365, 550, 240, 100, "readonly.yaml"),
    "sb_comp": (625, 550, 260, 100, "readonly-with-web.yaml"),
    "sb_repro": (915, 550, 260, 100, "readonly-with-local.yaml"),
}

# Box labels (list of lines)
LABELS = {
    "launcher": ["launcher/", "(auth + server startup)"],
    "gh_server": ["GitHub REST Server :8081", "(holds GH_TOKEN)"],
    "agent_runner": ["Agent Runner REST Server :8082", "(sandbox lifecycle)"],
    "github_api": ["GitHub API"],
    "gateway": ["OpenShell Gateway"],
    "triage": ["Triage Agent", "tools: curl (GH write + run-agent)"],
    "dup": ["Duplicate Detector", "tools: curl (GH read-only)"],
    "comp": ["Completeness Assessor", "tools: curl (GH read-only), WebFetch"],
    "repro": ["Reproducibility Verifier", "tools: curl (GH read-only), grep/find"],
    "ext_urls": ["External URLs"],
    "local_fs": ["Local filesystem"],
}

# ── Arrow definitions ────────────────────────────────────────────────
# (from_box, to_box, label, style, from_side, to_side)
# style: "solid", "dashed", "data"
# from_side/to_side: optional overrides, None = auto

ARROWS = {
    "launch_gh": ("launcher", "gh_server", "starts", "solid", None, None),
    "launch_runner": ("launcher", "agent_runner", "starts", "solid", None, None),
    "launch_triage": ("launcher", "agent_runner", "POST /run-agent\n(triage)", "solid", None, None),
    "runner_gateway": (
        "agent_runner",
        "gateway",
        "create, policy,\nSSH, delete",
        "solid",
        None,
        None,
    ),
    "gh_api": ("gh_server", "github_api", "scoped API calls", "solid", None, None),
    "runner_triage": ("agent_runner", "triage", "creates + runs", "solid", "bottom", "right"),
    "triage_gh": ("triage", "gh_server", "GET+POST :8081\nread-write", "dashed", "top", "bottom"),
    "triage_runner": ("triage", "agent_runner", "POST /run-agent", "solid", "top", "left"),
    "runner_dup": ("agent_runner", "dup", "creates + runs", "solid", None, None),
    "dup_gh": ("dup", "gh_server", "GET :8081\nread-only", "dashed", None, None),
    "runner_comp": ("agent_runner", "comp", "creates + runs", "solid", None, None),
    "comp_gh": ("comp", "gh_server", "GET :8081\nread-only", "dashed", None, None),
    "comp_web": ("comp", "ext_urls", "HTTPS GET", "solid", None, None),
    "runner_repro": ("agent_runner", "repro", "creates + runs\n(bugs only)", "solid", None, None),
    "repro_gh": ("repro", "gh_server", "GET :8081\nread-only", "dashed", None, None),
    "repro_fs": ("repro", "local_fs", "grep, find, cat", "solid", None, None),
    "dup_return": ("dup", "agent_runner", "JSON findings", "data", None, None),
    "comp_return": ("comp", "agent_runner", "JSON findings", "data", None, None),
    "repro_return": ("repro", "agent_runner", "JSON findings", "data", None, None),
    "runner_return": ("agent_runner", "triage", "JSON findings", "data", "bottom", "top"),
}


# ── Execution steps ──────────────────────────────────────────────────
# Each step: (title, new_boxes, new_sandboxes, new_arrows)
STEPS = [
    ("1. Launcher starts", ["launcher"], [], []),
    (
        "2. Start GitHub REST Server",
        ["gh_server"],
        [],
        ["launch_gh"],
    ),
    (
        "3. Start Agent Runner",
        ["agent_runner"],
        [],
        ["launch_runner"],
    ),
    (
        "4. Connect infrastructure",
        ["github_api", "gateway"],
        [],
        ["runner_gateway", "gh_api"],
    ),
    (
        "5. Launch Triage Agent",
        ["triage"],
        ["sb_triage"],
        ["launch_triage", "runner_triage"],
    ),
    (
        "6. Triage reads/writes GitHub",
        [],
        [],
        ["triage_gh"],
    ),
    (
        "7. Triage spawns Duplicate Detector",
        ["dup"],
        ["sb_dup"],
        ["triage_runner", "runner_dup"],
    ),
    (
        "8. Duplicate Detector reads GitHub",
        [],
        [],
        ["dup_gh"],
    ),
    (
        "9. Triage spawns Completeness Assessor",
        ["comp", "ext_urls"],
        ["sb_comp"],
        ["triage_runner", "runner_comp"],
    ),
    (
        "10. Completeness Assessor connects",
        [],
        [],
        ["comp_gh", "comp_web"],
    ),
    (
        "11. Triage spawns Reproducibility Verifier",
        ["repro", "local_fs"],
        ["sb_repro"],
        ["triage_runner", "runner_repro"],
    ),
    (
        "12. Reproducibility Verifier connects",
        [],
        [],
        ["repro_gh", "repro_fs"],
    ),
    (
        "13. Subagents return findings",
        [],
        [],
        ["dup_return", "comp_return", "repro_return", "runner_return"],
    ),
]


# ── Drawing helpers ──────────────────────────────────────────────────


def box_center(box_id: str) -> tuple[int, int]:
    x, y, w, h = BOXES[box_id]
    return x + w // 2, y + h // 2


def box_edge(box_id: str, side: str) -> tuple[int, int]:
    """Return midpoint of a box edge: top, bottom, left, right."""
    x, y, w, h = BOXES[box_id]
    if side == "top":
        return x + w // 2, y
    if side == "bottom":
        return x + w // 2, y + h
    if side == "left":
        return x, y + h // 2
    return x + w, y + h // 2  # right


def draw_box(
    draw: ImageDraw.ImageDraw,
    box_id: str,
    fill: tuple,
    text_color: tuple = COL_WHITE,
):
    x, y, w, h = BOXES[box_id]
    radius = 8
    draw.rounded_rectangle([x, y, x + w, y + h], radius=radius, fill=fill, outline=fill)
    lines = LABELS[box_id]
    # Vertically center text
    line_h = 16
    total_h = len(lines) * line_h
    ty = y + (h - total_h) // 2
    for i, line in enumerate(lines):
        font = FONT_BOLD if i == 0 else FONT_SMALL
        bbox = draw.textbbox((0, 0), line, font=font)
        tw = bbox[2] - bbox[0]
        tx = x + (w - tw) // 2
        draw.text((tx, ty + i * line_h), line, fill=text_color, font=font)


def draw_external_box(
    draw: ImageDraw.ImageDraw,
    box_id: str,
    fill: tuple,
):
    """Draw an external service node (rounded, grey)."""
    x, y, w, h = BOXES[box_id]
    draw.rounded_rectangle(
        [x, y, x + w, y + h], radius=12, fill=fill, outline=(180, 180, 180), width=2
    )
    lines = LABELS[box_id]
    line_h = 16
    total_h = len(lines) * line_h
    ty = y + (h - total_h) // 2
    for i, line in enumerate(lines):
        bbox = draw.textbbox((0, 0), line, font=FONT)
        tw = bbox[2] - bbox[0]
        tx = x + (w - tw) // 2
        draw.text((tx, ty + i * line_h), line, fill=COL_TEXT, font=FONT)


def draw_sandbox(
    draw: ImageDraw.ImageDraw,
    sb_id: str,
    is_new: bool = False,
):
    x, y, w, h, label = SANDBOXES[sb_id]
    border_w = 3 if is_new else 2
    border_col = COL_ARROW_NEW if is_new else COL_SANDBOX_BORDER
    draw.rounded_rectangle(
        [x, y, x + w, y + h],
        radius=10,
        fill=COL_SANDBOX,
        outline=border_col,
        width=border_w,
    )
    # Label at top
    draw.text((x + 8, y + 4), label, fill=(60, 120, 60), font=FONT_SMALL)


def _best_sides(from_id: str, to_id: str) -> tuple[str, str]:
    """Pick the best edge pair for an arrow between two boxes."""
    fx, fy, fw, fh = BOXES[from_id]
    tx, ty, tw, th = BOXES[to_id]
    fc = (fx + fw // 2, fy + fh // 2)
    tc = (tx + tw // 2, ty + th // 2)
    dx = tc[0] - fc[0]
    dy = tc[1] - fc[1]
    if abs(dy) > abs(dx):
        # Primarily vertical
        if dy > 0:
            return "bottom", "top"
        return "top", "bottom"
    # Primarily horizontal
    if dx > 0:
        return "right", "left"
    return "left", "right"


def draw_arrow(
    draw: ImageDraw.ImageDraw,
    from_id: str,
    to_id: str,
    label: str,
    style: str = "solid",
    is_new: bool = False,
    from_side_override: str | None = None,
    to_side_override: str | None = None,
):
    from_side, to_side = _best_sides(from_id, to_id)
    if from_side_override:
        from_side = from_side_override
    if to_side_override:
        to_side = to_side_override
    x1, y1 = box_edge(from_id, from_side)
    x2, y2 = box_edge(to_id, to_side)

    if is_new:
        col = COL_ARROW_NEW
    elif style == "data":
        col = COL_ARROW_DATA
    else:
        col = COL_ARROW

    line_w = 2

    if style == "dashed":
        # Draw dashed line manually
        _draw_dashed_line(draw, x1, y1, x2, y2, col, line_w, dash=10, gap=6)
    else:
        draw.line([(x1, y1), (x2, y2)], fill=col, width=line_w)

    # Arrowhead
    _draw_arrowhead(draw, x1, y1, x2, y2, col)

    # Label at midpoint, offset to the side of the line to avoid overlap
    if label:
        mx, my = (x1 + x2) // 2, (y1 + y2) // 2
        # Offset label perpendicular to the line direction
        import math

        dx = x2 - x1
        dy = y2 - y1
        length = math.sqrt(dx * dx + dy * dy)
        if length > 0:
            # Perpendicular offset (to the right of the arrow direction)
            px, py = -dy / length, dx / length
            offset = 14
            mx += int(px * offset)
            my += int(py * offset)
        lines = label.split("\n")
        for i, line in enumerate(lines):
            bbox = draw.textbbox((0, 0), line, font=FONT_SMALL)
            tw = bbox[2] - bbox[0]
            th = bbox[3] - bbox[1]
            lx = mx - tw // 2
            ly = my - len(lines) * (th + 2) // 2 + i * (th + 2) - 2
            # White background for readability
            draw.rectangle([lx - 2, ly - 1, lx + tw + 2, ly + th + 1], fill=(255, 255, 255, 220))
            draw.text((lx, ly), line, fill=col, font=FONT_SMALL)


def _draw_dashed_line(draw, x1, y1, x2, y2, col, width, dash=10, gap=6):
    import math

    dx = x2 - x1
    dy = y2 - y1
    length = math.sqrt(dx * dx + dy * dy)
    if length == 0:
        return
    ux, uy = dx / length, dy / length
    pos = 0
    drawing = True
    while pos < length:
        seg = dash if drawing else gap
        end = min(pos + seg, length)
        if drawing:
            sx = x1 + ux * pos
            sy = y1 + uy * pos
            ex = x1 + ux * end
            ey = y1 + uy * end
            draw.line([(sx, sy), (ex, ey)], fill=col, width=width)
        pos = end
        drawing = not drawing


def _draw_arrowhead(draw, x1, y1, x2, y2, col):
    import math

    dx = x2 - x1
    dy = y2 - y1
    length = math.sqrt(dx * dx + dy * dy)
    if length == 0:
        return
    ux, uy = dx / length, dy / length
    # Arrowhead size
    size = 10
    # Point at (x2, y2), wings perpendicular
    px, py = -uy, ux  # perpendicular
    points = [
        (x2, y2),
        (x2 - ux * size + px * size * 0.4, y2 - uy * size + py * size * 0.4),
        (x2 - ux * size - px * size * 0.4, y2 - uy * size - py * size * 0.4),
    ]
    draw.polygon(points, fill=col)


def draw_frame(step_index: int) -> Image.Image:
    """Draw a single frame showing all elements up to step_index."""
    img = Image.new("RGB", (W, H), BG)
    draw = ImageDraw.Draw(img)

    # Collect what's visible
    visible_boxes = set()
    visible_sandboxes = set()
    visible_arrows = set()
    new_boxes = set()
    new_sandboxes = set()
    new_arrows = set()

    for i in range(step_index + 1):
        title, boxes, sandboxes, arrows = STEPS[i]
        visible_boxes.update(boxes)
        visible_sandboxes.update(sandboxes)
        visible_arrows.update(arrows)
        if i == step_index:
            new_boxes = set(boxes)
            new_sandboxes = set(sandboxes)
            new_arrows = set(arrows)

    # Draw title
    title = STEPS[step_index][0]
    draw.text((20, 15), title, fill=COL_TEXT, font=FONT_TITLE)

    # Draw step indicator
    total = len(STEPS)
    indicator = f"Step {step_index + 1}/{total}"
    bbox = draw.textbbox((0, 0), indicator, font=FONT_SMALL)
    draw.text((W - (bbox[2] - bbox[0]) - 20, 18), indicator, fill=(150, 150, 150), font=FONT_SMALL)

    # Draw sandboxes first (background)
    for sb_id in visible_sandboxes:
        draw_sandbox(draw, sb_id, is_new=(sb_id in new_sandboxes))

    # Draw arrows — established first, then new (orange) on top
    for arrow_id in visible_arrows:
        if arrow_id in new_arrows:
            continue  # draw new arrows in second pass
        from_id, to_id, label, style, from_side, to_side = ARROWS[arrow_id]
        if from_id in visible_boxes and to_id in visible_boxes:
            draw_arrow(
                draw,
                from_id,
                to_id,
                label,
                style,
                is_new=False,
                from_side_override=from_side,
                to_side_override=to_side,
            )
    for arrow_id in new_arrows:
        if arrow_id not in visible_arrows:
            continue
        from_id, to_id, label, style, from_side, to_side = ARROWS[arrow_id]
        if from_id in visible_boxes and to_id in visible_boxes:
            draw_arrow(
                draw,
                from_id,
                to_id,
                label,
                style,
                is_new=True,
                from_side_override=from_side,
                to_side_override=to_side,
            )

    # Draw boxes
    external_nodes = {"github_api", "ext_urls", "local_fs", "gateway"}
    for box_id in visible_boxes:
        if box_id in external_nodes:
            fill = COL_NEW if box_id in new_boxes else COL_EXTERNAL
            draw_external_box(draw, box_id, fill)
        else:
            fill = COL_NEW if box_id in new_boxes else COL_ACTIVE
            draw_box(draw, box_id, fill)

    return img


def main():
    frames = []
    for i in range(len(STEPS)):
        print(f"Rendering frame {i}: {STEPS[i][0]}")
        frames.append(draw_frame(i))

    # Durations: 2.5s per frame, 5s on last frame
    durations = [2500] * len(frames)
    durations[-1] = 5000

    frames[0].save(
        OUTPUT_GIF,
        save_all=True,
        append_images=frames[1:],
        duration=durations,
        loop=0,
        optimize=True,
    )

    print(f"\nGIF saved to: {OUTPUT_GIF}")
    print(f"Frames: {len(frames)}, Size: {OUTPUT_GIF.stat().st_size / 1024:.0f} KB")


if __name__ == "__main__":
    main()
