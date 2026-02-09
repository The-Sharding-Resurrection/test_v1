"""
Baseline Protocol - Final Version
- Diagonal arrows that don't collide
- Arrow heads point right (time progression)
- Routing to specific target shards

PROTOCOL VERSION: This visualizes the experimental hop-based protocol from the
baseline_protocol branch, NOT the current main branch's 2PC implementation.
The baseline protocol uses iterative execution with NoStateError detection and
stateless orchestrator routing.
"""

from manim import *

SHARD_COLOR = "#1e3a8a"
BLOCK_COLOR = "#1e3a8a"
ERROR_COLOR = "#dc2626"
ROUTE_COLOR = WHITE
STATE_COLOR = "#22c55e"

class BaselineProtocol(Scene):
    def construct(self):
        self.camera.background_color = "#0a0a0a"
        self.camera.frame_width = 16

        title = Text("Baseline Protocol: Hop-Based Routing", font_size=26, color=WHITE)
        title.to_edge(UP, buff=0.3)
        self.add(title)

        # Labels
        orch_label = Text("Orchestrator\nShard", font_size=20, color=WHITE).move_to(LEFT*6 + UP*1.5)
        state1_label = Text("State\nShard 1", font_size=18, color=WHITE).move_to(LEFT*6)
        state2_label = Text("State\nShard 2", font_size=18, color=WHITE).move_to(LEFT*6 + DOWN*1.3)
        state3_label = Text("State\nShard 3", font_size=18, color=WHITE).move_to(LEFT*6 + DOWN*2.6)

        self.play(Write(orch_label), Write(state1_label), Write(state2_label), Write(state3_label))

        # Timeline lines
        orch_line = Line(LEFT*4.5 + UP*1.5, RIGHT*5.5 + UP*1.5, color=GRAY)
        state1_line = Line(LEFT*4.5, RIGHT*5.5, color=GRAY)
        state2_line = Line(LEFT*4.5 + DOWN*1.3, RIGHT*5.5 + DOWN*1.3, color=GRAY)
        state3_line = Line(LEFT*4.5 + DOWN*2.6, RIGHT*5.5 + DOWN*2.6, color=GRAY)

        self.play(Create(orch_line), Create(state1_line), Create(state2_line), Create(state3_line))
        self.wait(1)

        # ===== HOP 0 =====
        hop0 = Text("Hop 0: Shard 1 executes → external call", font_size=19, color=YELLOW)
        hop0.to_edge(UP, buff=0.3)
        self.play(Transform(title, hop0))

        # Contract on Shard 1
        contract1 = Rectangle(width=1.1, height=0.5, color=WHITE, fill_opacity=1, fill_color=WHITE)
        contract1.move_to(LEFT*3.5)
        contract1_text = Text("Travel", font_size=11, color=BLACK)
        contract1_text.move_to(contract1.get_center())

        self.play(FadeIn(contract1), Write(contract1_text))
        self.wait(0.5)

        # Error burst
        error1 = Star(n=8, outer_radius=0.35, color=ERROR_COLOR, fill_opacity=1)
        error1.next_to(contract1, RIGHT, buff=0.15)
        self.play(FadeIn(error1), run_time=0.3)
        self.wait(0.5)

        # Shard 1 block
        block1_s1 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block1_s1.move_to(LEFT*2.5)
        block1_s1_label = Text("1", font_size=13, color=WHITE)
        block1_s1_label.move_to(block1_s1.get_center())

        self.play(FadeIn(block1_s1), Write(block1_s1_label))
        self.wait(0.5)

        # Diagonal arrow UP-RIGHT (time progression)
        arrow1_up = Arrow(block1_s1.get_top() + RIGHT*0.1, LEFT*2.3 + UP*1.5,
                          color=STATE_COLOR, stroke_width=4, buff=0.1)
        self.play(Create(arrow1_up))
        self.wait(0.5)

        # Orchestrator block 1
        orch_block1 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        orch_block1.move_to(LEFT*2 + UP*1.5)
        orch_label1 = Text("1", font_size=13, color=WHITE)
        orch_label1.move_to(orch_block1.get_center())

        self.play(FadeIn(orch_block1), Write(orch_label1))
        self.wait(0.5)

        # Diagonal arrow DOWN-RIGHT to Shard 2 (time progression)
        route1 = Arrow(orch_block1.get_bottom() + RIGHT*0.2, LEFT*1.7 + DOWN*1.3,
                       color=ROUTE_COLOR, stroke_width=4, buff=0.1)

        self.play(Create(route1))
        self.wait(1)

        # Clean up
        self.play(
            FadeOut(contract1), FadeOut(contract1_text), FadeOut(error1),
            FadeOut(arrow1_up), FadeOut(route1)
        )

        # ===== HOP 1 =====
        hop1 = Text("Hop 1: Shard 2 re-executes → external call", font_size=19, color=YELLOW)
        hop1.to_edge(UP, buff=0.3)
        self.play(Transform(title, hop1))

        # Contract on Shard 2
        contract2 = Rectangle(width=1.1, height=0.5, color=WHITE, fill_opacity=1, fill_color=WHITE)
        contract2.move_to(LEFT*0.5 + DOWN*1.3)
        contract2_text = Text("Train", font_size=11, color=BLACK)
        contract2_text.move_to(contract2.get_center())

        self.play(FadeIn(contract2), Write(contract2_text))
        self.wait(0.5)

        # Error burst
        error2 = Star(n=8, outer_radius=0.35, color=ERROR_COLOR, fill_opacity=1)
        error2.next_to(contract2, RIGHT, buff=0.15)
        self.play(FadeIn(error2), run_time=0.3)
        self.wait(0.5)

        # Shard 2 block
        block1_s2 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block1_s2.move_to(RIGHT*0.5 + DOWN*1.3)
        block1_s2_label = Text("2", font_size=13, color=WHITE)
        block1_s2_label.move_to(block1_s2.get_center())

        self.play(FadeIn(block1_s2), Write(block1_s2_label))
        self.wait(0.5)

        # Diagonal arrow UP-RIGHT
        arrow2_up = Arrow(block1_s2.get_top() + RIGHT*0.1, RIGHT*0.7 + UP*1.5,
                          color=STATE_COLOR, stroke_width=4, buff=0.1)
        self.play(Create(arrow2_up))
        self.wait(0.5)

        # Orchestrator block 2
        orch_block2 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        orch_block2.move_to(RIGHT*1 + UP*1.5)
        orch_label2 = Text("2", font_size=13, color=WHITE)
        orch_label2.move_to(orch_block2.get_center())

        self.play(FadeIn(orch_block2), Write(orch_label2))
        self.wait(0.5)

        # Diagonal arrow DOWN-RIGHT to Shard 3
        route2 = Arrow(orch_block2.get_bottom() + RIGHT*0.2, RIGHT*1.3 + DOWN*2.6,
                       color=ROUTE_COLOR, stroke_width=4, buff=0.1)

        self.play(Create(route2))
        self.wait(1)

        # Clean up
        self.play(
            FadeOut(contract2), FadeOut(contract2_text), FadeOut(error2),
            FadeOut(arrow2_up), FadeOut(route2)
        )

        # ===== HOP 2 =====
        hop2 = Text("Hop 2: Shard 3 completes → SUCCESS", font_size=19, color="#22c55e")
        hop2.to_edge(UP, buff=0.3)
        self.play(Transform(title, hop2))

        # Contract on Shard 3
        contract3 = Rectangle(width=1.1, height=0.5, color=WHITE, fill_opacity=1, fill_color=WHITE)
        contract3.move_to(RIGHT*2.5 + DOWN*2.6)
        contract3_text = Text("Hotel", font_size=11, color=BLACK)
        contract3_text.move_to(contract3.get_center())

        self.play(FadeIn(contract3), Write(contract3_text))
        self.wait(0.5)

        # Success mark
        success = Text("✓", font_size=24, color="#22c55e")
        success.next_to(contract3, RIGHT, buff=0.2)
        self.play(Write(success))
        self.wait(0.5)

        # Shard 3 block
        block1_s3 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block1_s3.move_to(RIGHT*3.5 + DOWN*2.6)
        block1_s3_label = Text("3", font_size=13, color=WHITE)
        block1_s3_label.move_to(block1_s3.get_center())

        self.play(FadeIn(block1_s3), Write(block1_s3_label))
        self.wait(0.5)

        # Diagonal arrow UP-RIGHT to orchestrator
        arrow3_up = Arrow(block1_s3.get_top() + RIGHT*0.1, RIGHT*3.7 + UP*1.5,
                          color=STATE_COLOR, stroke_width=4, buff=0.1)
        self.play(Create(arrow3_up))
        self.wait(0.5)

        # Orchestrator sees SUCCESS
        orch_block3 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        orch_block3.move_to(RIGHT*4 + UP*1.5)
        orch_label3 = Text("3", font_size=13, color=WHITE)
        orch_label3.move_to(orch_block3.get_center())

        self.play(FadeIn(orch_block3), Write(orch_label3))
        self.wait(1)

        # Summary
        summary = Text(
            "Baseline: Stateless routing, 3 hops",
            font_size=18,
            color="#22c55e"
        ).to_edge(DOWN, buff=0.5)

        self.play(Write(summary))
        self.wait(3)


if __name__ == "__main__":
    pass
