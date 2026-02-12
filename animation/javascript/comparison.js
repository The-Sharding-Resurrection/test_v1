/**
 * Block Flow Chart Comparison Visualization
 * Stacked: Baseline (13 block steps) vs 2PC Matrix (4 block steps)
 * Both animate on the same clock so the viewer sees 2PC finish first.
 */

class ComparisonVisualization {
    constructor() {
        this.svg = d3.select('#comparison-svg');
        if (this.svg.empty()) return;

        this.currentStep = 0;
        this.isPlaying = false;
        this.speed = 1.0;
        this.timer = null;

        // ── Layout constants ──
        this.X0 = 180;       // first step X
        this.DX = 70;        // step spacing
        this.BLK = 24;       // block size
        this.H = 12;         // half block

        // Baseline chart (top)
        this.blLanes = [80, 130, 180, 230];
        // 2PC chart (bottom)
        this.tpLanes = [380, 430, 480, 530];

        this.LABELS = ['Orch Shard', 'Travel (A)', 'Train (B)', 'Hotel (C)'];

        // Colors
        this.ORCH   = '#C0392B';  // red
        this.SHARD  = '#1B2631';  // dark navy
        this.ARROW  = '#777';
        this.RPC    = '#aaa';
        this.DONE   = '#27ae60';

        this.initData();
        this.initSVG();
        this.bindControls();
    }

    /* stepX: 1-based step number → X coordinate */
    sx(n) { return this.X0 + (n - 1) * this.DX; }

    blockFill(lane) { return lane === 0 ? this.ORCH : this.SHARD; }

    // ── Data ──────────────────────────────────────────────────────────

    initData() {
        // Each step: { blocks: [lane, ...], arrows: [{from, fl, tl}] }
        //   from = source step (1-based), fl = from lane, tl = to lane
        this.blSteps = [
            { blocks: [1] },                                                                              //  1
            { blocks: [0], arrows: [{from:1,  fl:1, tl:0}] },                                            //  2
            { blocks: [2], arrows: [{from:2,  fl:0, tl:2}] },                                            //  3
            { blocks: [0], arrows: [{from:3,  fl:2, tl:0}] },                                            //  4
            { blocks: [3], arrows: [{from:4,  fl:0, tl:3}] },                                            //  5
            { blocks: [0], arrows: [{from:5,  fl:3, tl:0}] },                                            //  6
            { blocks: [2], arrows: [{from:6,  fl:0, tl:2}] },                                            //  7
            { blocks: [0], arrows: [{from:7,  fl:2, tl:0}] },                                            //  8
            { blocks: [3], arrows: [{from:8,  fl:0, tl:3}] },                                            //  9
            { blocks: [0], arrows: [{from:9,  fl:3, tl:0}] },                                            // 10
            { blocks: [1], arrows: [{from:10, fl:0, tl:1}] },                                            // 11
            { blocks: [0], arrows: [{from:11, fl:1, tl:0}] },                                            // 12
            { blocks: [1, 2, 3], arrows: [{from:12,fl:0,tl:1},{from:12,fl:0,tl:2},{from:12,fl:0,tl:3}] } // 13
        ];
        this.blSteps.forEach(s => { if (!s.arrows) s.arrows = []; });

        this.tpSteps = [
            { blocks: [0], arrows: [] },                                                                  //  1
            { blocks: [1, 2, 3], arrows: [{from:1,fl:0,tl:1},{from:1,fl:0,tl:2},{from:1,fl:0,tl:3}] },  //  2
            { blocks: [0], arrows: [{from:2,fl:1,tl:0},{from:2,fl:2,tl:0},{from:2,fl:3,tl:0}] },         //  3
            { blocks: [1, 2, 3], arrows: [{from:3,fl:0,tl:1},{from:3,fl:0,tl:2},{from:3,fl:0,tl:3}] }    //  4
        ];
    }

    // ── SVG setup ─────────────────────────────────────────────────────

    initSVG() {
        this.svg
            .attr('viewBox', '0 0 1200 700')
            .attr('preserveAspectRatio', 'xMidYMid meet');
        this.svg.selectAll('*').remove();

        // White background
        this.svg.append('rect')
            .attr('width', 1200).attr('height', 700).attr('fill', '#fff');

        // Arrow markers
        const defs = this.svg.append('defs');
        this._marker(defs, 'arr-solid', this.ARROW);
        this._marker(defs, 'arr-dash',  this.RPC);

        // Static elements
        this.drawStatic();

        // Dynamic groups (cleared on each render)
        this.gBL     = this.svg.append('g');
        this.gTP     = this.svg.append('g');
        this.gStatus = this.svg.append('g');
    }

    _marker(defs, id, fill) {
        defs.append('marker')
            .attr('id', id)
            .attr('viewBox', '0 -4 8 8')
            .attr('refX', 7).attr('refY', 0)
            .attr('markerWidth', 7).attr('markerHeight', 7)
            .attr('orient', 'auto')
            .append('path')
            .attr('d', 'M0,-3L7,0L0,3Z')
            .attr('fill', fill);
    }

    // ── Static drawing ────────────────────────────────────────────────

    drawStatic() {
        const s = this.svg;

        // ── Baseline section ──
        s.append('text')
            .attr('x', 20).attr('y', 38)
            .attr('font-size', '15px').attr('font-weight', '700').attr('fill', '#333')
            .text('Baseline Protocol (Iterative Re-execution)');

        this.drawLanes(s, this.blLanes);
        this.drawStepNumbers(this.blLanes[0] - 22);

        // ── Divider ──
        s.append('line')
            .attr('x1', 20).attr('y1', 300).attr('x2', 1180).attr('y2', 300)
            .attr('stroke', '#ddd').attr('stroke-width', 1);

        // ── 2PC section ──
        s.append('text')
            .attr('x', 20).attr('y', 338)
            .attr('font-size', '15px').attr('font-weight', '700').attr('fill', '#333')
            .text('2PC Protocol (Matrix)');

        this.drawLanes(s, this.tpLanes);
        this.drawStepNumbers(this.tpLanes[0] - 22);

        // ── Legend ──
        this.drawLegend(s);
    }

    drawLanes(s, lanes) {
        const endX = this.sx(13) + 35;
        lanes.forEach((y, i) => {
            s.append('text')
                .attr('x', 115).attr('y', y + 4)
                .attr('text-anchor', 'end')
                .attr('font-size', '11px').attr('fill', '#555')
                .text(this.LABELS[i]);
            s.append('line')
                .attr('x1', 125).attr('y1', y)
                .attr('x2', endX).attr('y2', y)
                .attr('stroke', '#eee').attr('stroke-width', 1)
                .attr('stroke-dasharray', '4,4');
        });
    }

    drawStepNumbers(y) {
        for (let i = 1; i <= 13; i++) {
            this.svg.append('text')
                .attr('x', this.sx(i)).attr('y', y)
                .attr('text-anchor', 'middle')
                .attr('font-size', '9px').attr('fill', '#bbb')
                .text(i);
        }
    }

    drawLegend(s) {
        const g = s.append('g').attr('transform', 'translate(930, 610)');

        g.append('rect')
            .attr('x', -10).attr('y', -12)
            .attr('width', 260).attr('height', 80)
            .attr('fill', '#fafafa').attr('stroke', '#ddd').attr('rx', 4);

        // Orch block
        g.append('rect').attr('x', 0).attr('y', 0)
            .attr('width', 14).attr('height', 14)
            .attr('fill', this.ORCH).attr('rx', 2);
        g.append('text').attr('x', 22).attr('y', 11)
            .attr('font-size', '11px').attr('fill', '#555')
            .text('Orchestration Shard Block');

        // State block
        g.append('rect').attr('x', 0).attr('y', 22)
            .attr('width', 14).attr('height', 14)
            .attr('fill', this.SHARD).attr('rx', 2);
        g.append('text').attr('x', 22).attr('y', 33)
            .attr('font-size', '11px').attr('fill', '#555')
            .text('State Shard Block');

        // Solid arrow
        g.append('line')
            .attr('x1', 0).attr('y1', 50).attr('x2', 14).attr('y2', 50)
            .attr('stroke', this.ARROW).attr('stroke-width', 1.5);
        g.append('text').attr('x', 22).attr('y', 53)
            .attr('font-size', '11px').attr('fill', '#555')
            .text('Block propagation');

        // Dashed arrow
        g.append('line')
            .attr('x1', 135).attr('y1', 50).attr('x2', 149).attr('y2', 50)
            .attr('stroke', this.RPC).attr('stroke-width', 1.5)
            .attr('stroke-dasharray', '4,3');
        g.append('text').attr('x', 157).attr('y', 53)
            .attr('font-size', '11px').attr('fill', '#555')
            .text('RPC (HTTP)');
    }

    // ── Dynamic rendering ─────────────────────────────────────────────

    render() {
        this.gBL.selectAll('*').remove();
        this.gTP.selectAll('*').remove();
        this.gStatus.selectAll('*').remove();

        // Baseline steps
        const blN = Math.min(this.currentStep, this.blSteps.length);
        for (let i = 0; i < blN; i++) {
            this.renderStep(this.gBL, this.blSteps[i], i + 1, this.blLanes);
        }

        // 2PC: RPC simulation arrows (visible from step 1)
        if (this.currentStep >= 1) {
            this.renderRPC();
        }
        // 2PC steps
        const tpN = Math.min(this.currentStep, this.tpSteps.length);
        for (let i = 0; i < tpN; i++) {
            this.renderStep(this.gTP, this.tpSteps[i], i + 1, this.tpLanes);
        }

        // Completion labels
        if (this.currentStep >= 4) {
            this.renderDone(this.tpLanes, 4, '4 block times');
        }
        if (this.currentStep >= 13) {
            this.renderDone(this.blLanes, 13, '13 block times');
            this.renderSummary();
        }
    }

    renderStep(g, step, num, lanes) {
        const cx = this.sx(num);

        // Arrows (drawn behind blocks)
        step.arrows.forEach(a => {
            const fx = this.sx(a.from) + this.H;
            const fy = lanes[a.fl];
            const tx = cx - this.H - 2;
            const ty = lanes[a.tl];

            g.append('line')
                .attr('x1', fx).attr('y1', fy)
                .attr('x2', tx).attr('y2', ty)
                .attr('stroke', this.ARROW).attr('stroke-width', 1.5)
                .attr('marker-end', 'url(#arr-solid)');
        });

        // Blocks
        step.blocks.forEach(lane => {
            g.append('rect')
                .attr('x', cx - this.H).attr('y', lanes[lane] - this.H)
                .attr('width', this.BLK).attr('height', this.BLK)
                .attr('fill', this.blockFill(lane)).attr('rx', 3);
        });
    }

    renderRPC() {
        // Dashed arrows showing HTTP simulation (state fetch) before block 1
        const ox = this.X0 - 35;            // X origin for simulation arrows
        const oy = this.tpLanes[0];         // Orch Y

        [1, 2, 3].forEach((lane, i) => {
            const dx = ox + (i - 1) * 12;   // fan out horizontally
            const sy = this.tpLanes[lane];

            this.gTP.append('line')
                .attr('x1', dx).attr('y1', oy + 6)
                .attr('x2', dx).attr('y2', sy - 6)
                .attr('stroke', this.RPC).attr('stroke-width', 1)
                .attr('stroke-dasharray', '4,3')
                .attr('marker-end', 'url(#arr-dash)');
        });

        // Label
        this.gTP.append('text')
            .attr('x', ox).attr('y', oy - 10)
            .attr('text-anchor', 'middle')
            .attr('font-size', '9px').attr('fill', '#aaa')
            .attr('font-style', 'italic')
            .text('HTTP sim');
    }

    renderDone(lanes, stepNum, label) {
        const x = this.sx(stepNum) + 42;
        const y = (lanes[0] + lanes[3]) / 2;

        this.gStatus.append('text')
            .attr('x', x).attr('y', y - 6)
            .attr('font-size', '13px').attr('font-weight', '700').attr('fill', this.DONE)
            .text('COMPLETE');
        this.gStatus.append('text')
            .attr('x', x).attr('y', y + 12)
            .attr('font-size', '11px').attr('fill', '#666')
            .text(label);
    }

    renderSummary() {
        this.gStatus.append('text')
            .attr('x', 600).attr('y', 680)
            .attr('text-anchor', 'middle')
            .attr('font-size', '16px').attr('font-weight', '700').attr('fill', this.DONE)
            .text('Matrix (2PC): 3.25\u00d7 faster');
    }

    // ── Animation controls ────────────────────────────────────────────

    play() {
        if (this.isPlaying) return;
        this.isPlaying = true;
        this._tick();
    }

    _tick() {
        if (!this.isPlaying || this.currentStep >= 13) {
            this.isPlaying = false;
            return;
        }
        this.step();
        this.timer = setTimeout(() => this._tick(), 800 / this.speed);
    }

    pause() {
        this.isPlaying = false;
        if (this.timer) { clearTimeout(this.timer); this.timer = null; }
    }

    step() {
        if (this.currentStep >= 13) return;
        this.currentStep++;
        this.render();
    }

    reset() {
        this.pause();
        this.currentStep = 0;
        this.render();
    }

    bindControls() {
        const on = (id, fn) => {
            const el = document.getElementById(id);
            if (el) el.addEventListener('click', fn);
        };
        on('comparison-play',  () => this.play());
        on('comparison-pause', () => this.pause());
        on('comparison-step',  () => this.step());
        on('comparison-reset', () => this.reset());

        const sp = document.getElementById('comparison-speed');
        if (sp) sp.addEventListener('input', e => { this.speed = parseFloat(e.target.value); });
    }
}

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', () => { new ComparisonVisualization(); });
