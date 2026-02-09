"""
2PC Protocol - Final Version
- Diagonal arrows that don't collide
- Arrow heads point right (time progression)
- Broadcasts to all shards with visible arrows

PROTOCOL VERSION: This visualizes the block-based two-phase commit protocol
from the main branch. Uses simulation-based execution with PREPARE/COMMIT
voting phases for atomic cross-shard transactions. See docs/2pc-protocol.md.
"""

from manim import *

SHARD_COLOR = "#1e3a8a"
BLOCK_COLOR = "#1e3a8a"
BROADCAST_COLOR = WHITE
STATE_COLOR = "#22c55e"

class TwoPCProtocol(Scene):
    def construct(self):
        self.camera.background_color = "#0a0a0a"
        self.camera.frame_width = 16

        title = Text("2PC Protocol: Block-Based Two-Phase Commit", font_size=24, color=WHITE)
        title.to_edge(UP, buff=0.3)
        self.add(title)

        # Data structure labels
        top_left_box = Rectangle(width=2.8, height=0.7, color=WHITE, stroke_width=2)
        top_left_box.move_to(RIGHT*2 + UP*3)
        top_left_text = Text(
            "tpc_result    map[Hash] bool\nct_to_order   []CrossShardTx",
            font_size=9, color=WHITE
        )
        top_left_text.move_to(top_left_box.get_center())

        top_right_box = Rectangle(width=2.4, height=0.7, color=WHITE, stroke_width=2)
        top_right_box.move_to(RIGHT*5 + UP*3)
        top_right_text = Text(
            "tx_ordering   []Transaction\ntpc_prepare   map[Hash] bool",
            font_size=9, color=WHITE
        )
        top_right_text.move_to(top_right_box.get_center())

        self.add(top_left_box, top_left_text, top_right_box, top_right_text)

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

        # Green arrows (state request/reply) - static
        green1 = Arrow(LEFT*4.7, LEFT*5 + UP*1.5, color=STATE_COLOR, stroke_width=3, buff=0.1)
        green2 = Arrow(LEFT*4.7 + DOWN*1.3, LEFT*5 + UP*1.5, color=STATE_COLOR, stroke_width=3, buff=0.1)
        green3 = Arrow(LEFT*4.7 + DOWN*2.6, LEFT*5 + UP*1.5, color=STATE_COLOR, stroke_width=3, buff=0.1)

        self.play(Create(green1), Create(green2), Create(green3))
        self.wait(1)

        # ===== BLOCK 1: Tx Submission =====
        phase1 = Text("Block 1: Tx submitted, simulation complete", font_size=20, color=YELLOW)
        phase1.to_edge(UP, buff=0.3)
        self.play(Transform(title, phase1))

        # Orchestrator block 1
        block1 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block1.move_to(LEFT*3.5 + UP*1.5)
        block1_label = Text("1", font_size=13, color=WHITE)
        block1_label.move_to(block1.get_center())

        self.play(FadeIn(block1), Write(block1_label))
        self.wait(0.5)

        # Broadcast PREPARE - three diagonal arrows DOWN-RIGHT (don't collide)
        # Spread them out by starting at different x positions
        broadcast1_1 = Arrow(block1.get_bottom() + RIGHT*0.05, LEFT*3.4 + UP*0.05,
                            color=BROADCAST_COLOR, stroke_width=4, buff=0.1)
        broadcast1_2 = Arrow(block1.get_bottom() + RIGHT*0.15, LEFT*3.2 + DOWN*1.25,
                            color=BROADCAST_COLOR, stroke_width=4, buff=0.1)
        broadcast1_3 = Arrow(block1.get_bottom() + RIGHT*0.25, LEFT*3.0 + DOWN*2.55,
                            color=BROADCAST_COLOR, stroke_width=4, buff=0.1)

        self.play(Create(broadcast1_1), Create(broadcast1_2), Create(broadcast1_3))
        self.wait(1)
        self.play(FadeOut(broadcast1_1), FadeOut(broadcast1_2), FadeOut(broadcast1_3))

        # ===== BLOCK 2: PREPARE Phase =====
        phase2 = Text("Block 2: State shards lock & vote", font_size=20, color=YELLOW)
        phase2.to_edge(UP, buff=0.3)
        self.play(Transform(title, phase2))

        # State shard blocks
        block2_s1 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block2_s1.move_to(LEFT*2)
        block2_s1_label = Text("2", font_size=13, color=WHITE)
        block2_s1_label.move_to(block2_s1.get_center())

        block2_s2 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block2_s2.move_to(LEFT*2 + DOWN*1.3)
        block2_s2_label = Text("2", font_size=13, color=WHITE)
        block2_s2_label.move_to(block2_s2.get_center())

        block2_s3 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block2_s3.move_to(LEFT*2 + DOWN*2.6)
        block2_s3_label = Text("2", font_size=13, color=WHITE)
        block2_s3_label.move_to(block2_s3.get_center())

        self.play(
            FadeIn(block2_s1), Write(block2_s1_label),
            FadeIn(block2_s2), Write(block2_s2_label),
            FadeIn(block2_s3), Write(block2_s3_label)
        )
        self.wait(0.5)

        # Votes UP-RIGHT to orchestrator (spread out to not collide)
        vote1 = Arrow(block2_s1.get_top() + RIGHT*0.05, LEFT*1.85 + UP*1.45,
                     color=BROADCAST_COLOR, stroke_width=4, buff=0.1)
        vote2 = Arrow(block2_s2.get_top() + RIGHT*0.15 + UP*0.05, LEFT*1.7 + UP*1.4,
                     color=BROADCAST_COLOR, stroke_width=4, buff=0.1)
        vote3 = Arrow(block2_s3.get_top() + RIGHT*0.25 + UP*0.1, LEFT*1.55 + UP*1.35,
                     color=BROADCAST_COLOR, stroke_width=4, buff=0.1)

        self.play(Create(vote1), Create(vote2), Create(vote3))
        self.wait(1)
        self.play(FadeOut(vote1), FadeOut(vote2), FadeOut(vote3))

        # ===== BLOCK 3: COMMIT Decision =====
        phase3 = Text("Block 3: Orchestrator broadcasts COMMIT", font_size=20, color="#22c55e")
        phase3.to_edge(UP, buff=0.3)
        self.play(Transform(title, phase3))

        # Orchestrator block 3
        block3 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block3.move_to(LEFT*0.5 + UP*1.5)
        block3_label = Text("3", font_size=13, color=WHITE)
        block3_label.move_to(block3.get_center())

        self.play(FadeIn(block3), Write(block3_label))
        self.wait(0.5)

        # Broadcast COMMIT - diagonal DOWN-RIGHT
        broadcast3_1 = Arrow(block3.get_bottom() + RIGHT*0.05, LEFT*0.4 + UP*0.05,
                            color=BROADCAST_COLOR, stroke_width=4, buff=0.1)
        broadcast3_2 = Arrow(block3.get_bottom() + RIGHT*0.15, LEFT*0.2 + DOWN*1.25,
                            color=BROADCAST_COLOR, stroke_width=4, buff=0.1)
        broadcast3_3 = Arrow(block3.get_bottom() + RIGHT*0.25, RIGHT*0.0 + DOWN*2.55,
                            color=BROADCAST_COLOR, stroke_width=4, buff=0.1)

        self.play(Create(broadcast3_1), Create(broadcast3_2), Create(broadcast3_3))
        self.wait(1)
        self.play(FadeOut(broadcast3_1), FadeOut(broadcast3_2), FadeOut(broadcast3_3))

        # ===== BLOCK 4: Finalize =====
        phase4 = Text("Block 4: All shards finalize", font_size=20, color="#22c55e")
        phase4.to_edge(UP, buff=0.3)
        self.play(Transform(title, phase4))

        # Final blocks
        block4_s1 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block4_s1.move_to(RIGHT*1)
        block4_s1_label = Text("4", font_size=13, color=WHITE)
        block4_s1_label.move_to(block4_s1.get_center())

        block4_s2 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block4_s2.move_to(RIGHT*1 + DOWN*1.3)
        block4_s2_label = Text("4", font_size=13, color=WHITE)
        block4_s2_label.move_to(block4_s2.get_center())

        block4_s3 = Square(side_length=0.7, color=BLOCK_COLOR, fill_opacity=1)
        block4_s3.move_to(RIGHT*1 + DOWN*2.6)
        block4_s3_label = Text("4", font_size=13, color=WHITE)
        block4_s3_label.move_to(block4_s3.get_center())

        self.play(
            FadeIn(block4_s1), Write(block4_s1_label),
            FadeIn(block4_s2), Write(block4_s2_label),
            FadeIn(block4_s3), Write(block4_s3_label)
        )
        self.wait(1)

        # Legend
        legend_y = DOWN*3.3
        legend_title = Text("Legend:", font_size=13, weight=BOLD, color=WHITE).move_to(LEFT*5 + legend_y)

        legend_block = Square(side_length=0.3, color=BLOCK_COLOR, fill_opacity=1)
        legend_block.move_to(LEFT*3.8 + legend_y)
        legend_block_text = Text("Finalized Block", font_size=11, color=WHITE)
        legend_block_text.next_to(legend_block, RIGHT, buff=0.2)

        legend_white = Arrow(ORIGIN, RIGHT*0.4, color=BROADCAST_COLOR, stroke_width=3)
        legend_white.move_to(LEFT*1.5 + legend_y)
        legend_white_text = Text("Block Broadcast", font_size=11, color=WHITE)
        legend_white_text.next_to(legend_white, RIGHT, buff=0.2)

        legend_green = Arrow(ORIGIN, RIGHT*0.4, color=STATE_COLOR, stroke_width=3)
        legend_green.move_to(RIGHT*1.3 + legend_y)
        legend_green_text = Text("State Request/Reply", font_size=11, color=WHITE)
        legend_green_text.next_to(legend_green, RIGHT, buff=0.2)

        self.play(
            Write(legend_title),
            FadeIn(legend_block), Write(legend_block_text),
            Create(legend_white), Write(legend_white_text),
            Create(legend_green), Write(legend_green_text)
        )
        self.wait(1)

        # Summary
        summary = Text(
            "2PC: 2 rounds, atomic guarantee via voting",
            font_size=17,
            color="#22c55e"
        ).to_edge(DOWN, buff=0.5)

        self.play(Write(summary))
        self.wait(3)


if __name__ == "__main__":
    pass
