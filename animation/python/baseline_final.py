"""
Baseline Protocol - Final Version
12-step iterative execution with orchestrator routing.
Steps: travel → orch → train → orch → hotel → orch → train → orch → hotel → orch → travel → orch → COMPLETE

PROTOCOL VERSION: This visualizes the experimental hop-based protocol from the
baseline_protocol branch, NOT the current main branch's 2PC implementation.
The baseline protocol uses iterative execution with NoStateError detection and
stateless orchestrator routing.
"""

from manim import *

SHARD_COLOR = "#8b5cf6"
BLOCK_COLOR = "#8b5cf6"
ORCH_COLOR  = "#ef4444"
ERROR_COLOR = "#ef4444"
ROUTE_COLOR = WHITE
STATE_COLOR = "#22c55e"

# Lane Y positions
ORCH_Y   = UP * 1.5
TRAVEL_Y = ORIGIN
TRAIN_Y  = DOWN * 1.3
HOTEL_Y  = DOWN * 2.6

class BaselineProtocol(Scene):
    def construct(self):
        self.camera.background_color = "#0d0d1a"
        self.camera.frame_width = 16

        title = Text("Baseline Protocol: 12-Step Iterative Execution", font_size=24, color=WHITE)
        title.to_edge(UP, buff=0.3)
        self.add(title)

        # Labels
        orch_label = Text("Orch\nShard", font_size=18, color=WHITE).move_to(LEFT * 6.5 + ORCH_Y)
        travel_label = Text("Travel\nShard", font_size=18, color=WHITE).move_to(LEFT * 6.5 + TRAVEL_Y)
        train_label = Text("Train\nShard", font_size=18, color=WHITE).move_to(LEFT * 6.5 + TRAIN_Y)
        hotel_label = Text("Hotel\nShard", font_size=18, color=WHITE).move_to(LEFT * 6.5 + HOTEL_Y)

        self.play(
            Write(orch_label), Write(travel_label),
            Write(train_label), Write(hotel_label)
        )

        # Timeline lines
        line_start = LEFT * 5.2
        line_end = RIGHT * 6.5
        orch_line = Line(line_start + ORCH_Y, line_end + ORCH_Y, color=GRAY)
        travel_line = Line(line_start + TRAVEL_Y, line_end + TRAVEL_Y, color=GRAY)
        train_line = Line(line_start + TRAIN_Y, line_end + TRAIN_Y, color=GRAY)
        hotel_line = Line(line_start + HOTEL_Y, line_end + HOTEL_Y, color=GRAY)

        self.play(
            Create(orch_line), Create(travel_line),
            Create(train_line), Create(hotel_line)
        )
        self.wait(0.5)

        # X positions for each of the 12 steps (evenly spaced)
        x_start = -4.5
        x_step = 0.85
        def sx(n):
            return LEFT * (-x_start - (n - 1) * x_step)

        # Lane Y map
        lane_y = {
            'orch': ORCH_Y, 'travel': TRAVEL_Y,
            'train': TRAIN_Y, 'hotel': HOTEL_Y
        }

        # Step definitions: (lane, label_text, step_title)
        steps = [
            ('travel', '1',  'Step 1: Travel executes → NoStateError: checkSeat()'),
            ('orch',   '2',  'Step 2: Orch routes to Train'),
            ('train',  '3',  'Step 3: Train executes checkSeat() → OK'),
            ('orch',   '4',  'Step 4: Orch routes to Hotel'),
            ('hotel',  '5',  'Step 5: Hotel executes checkRoom() → OK'),
            ('orch',   '6',  'Step 6: Orch routes to Train'),
            ('train',  '7',  'Step 7: Train executes bookTrain() → OK'),
            ('orch',   '8',  'Step 8: Orch routes to Hotel'),
            ('hotel',  '9',  'Step 9: Hotel executes bookHotel() → OK'),
            ('orch',   '10', 'Step 10: Orch routes back to Travel'),
            ('travel', '11', 'Step 11: Travel finalizes → SUCCESS'),
            ('orch',   '12', 'Step 12: Orch commits → COMPLETE'),
        ]

        blocks = []
        prev_pos = None
        prev_lane = None

        for i, (lane, label, step_title) in enumerate(steps):
            num = i + 1
            pos = sx(num) + lane_y[lane]
            is_orch = (lane == 'orch')
            color = ORCH_COLOR if is_orch else BLOCK_COLOR

            # Update title
            title_color = STATE_COLOR if num >= 11 else (YELLOW if is_orch else WHITE)
            new_title = Text(step_title, font_size=18, color=title_color)
            new_title.to_edge(UP, buff=0.3)
            self.play(Transform(title, new_title), run_time=0.3)

            # Draw arrow from previous block
            if prev_pos is not None:
                arrow = Arrow(
                    prev_pos + RIGHT * 0.4,
                    pos + LEFT * 0.4,
                    color=ROUTE_COLOR if is_orch else STATE_COLOR,
                    stroke_width=3, buff=0.05
                )
                self.play(Create(arrow), run_time=0.3)

            # Draw block
            block = Square(side_length=0.6, color=color, fill_opacity=1)
            block.move_to(pos)
            block_label = Text(label, font_size=11, color=WHITE)
            block_label.move_to(block.get_center())

            self.play(FadeIn(block), Write(block_label), run_time=0.3)
            blocks.append((block, block_label))

            # Special decorations
            if num == 1:
                # Error burst for NoStateError
                error = Star(n=8, outer_radius=0.25, color=ERROR_COLOR, fill_opacity=1)
                error.next_to(block, RIGHT, buff=0.1)
                self.play(FadeIn(error), run_time=0.2)

            if num == 11:
                # Success checkmark
                check = Text("✓", font_size=20, color=STATE_COLOR)
                check.next_to(block, RIGHT, buff=0.1)
                self.play(Write(check), run_time=0.3)

            self.wait(0.3)
            prev_pos = pos
            prev_lane = lane

        # Final COMPLETE text
        complete = Text("COMPLETE", font_size=22, color=STATE_COLOR, weight=BOLD)
        complete.next_to(blocks[-1][0], RIGHT, buff=0.5)
        self.play(Write(complete))

        # Summary
        summary = Text(
            "Baseline: 12 block steps, iterative routing",
            font_size=18,
            color=STATE_COLOR
        ).to_edge(DOWN, buff=0.5)

        self.play(Write(summary))
        self.wait(3)


if __name__ == "__main__":
    pass
