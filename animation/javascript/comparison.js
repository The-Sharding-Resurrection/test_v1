/**
 * Block Flow Chart Comparison Visualization
 * Stacked: Baseline (19 block steps) vs 2PC Matrix (4 block steps)
 * Both animate on the same clock. 2PC cycles every 4 steps while baseline
 * takes all 19. Animation loops forever with committed transaction counters.
 */

class ComparisonVisualization {
    constructor() {
        this.svg = d3.select('#comparison-svg');
        if (this.svg.empty()) return;

        this.currentStep = 0;
        this.isPlaying = false;
        this.speed = 2.0;
        this.timer = null;
        this.BL_TOTAL = 19;
        this.TP_TOTAL = 4;

        // Commit counters (persist across loops)
        this.blCommitCount = 0;
        this.tpCommitCount = 0;

        // ── Layout constants ──
        this.X0 = 140;       // first step X
        this.DX = 55;        // step spacing
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
        // Lanes: 0=Orch, 1=Travel(A), 2=Train(B), 3=Hotel(C)
        this.blSteps = [
            { blocks: [1] },                                                                                //  1  Travel: exec, NoState checkSeat
            { blocks: [0], arrows: [{from:1,  fl:1, tl:0}] },                                              //  2  Orch: route to Train
            { blocks: [2], arrows: [{from:2,  fl:0, tl:2}] },                                              //  3  Train: checkSeat OK
            { blocks: [0], arrows: [{from:3,  fl:2, tl:0}] },                                              //  4  Orch: route back to Travel
            { blocks: [1], arrows: [{from:4,  fl:0, tl:1}] },                                              //  5  Travel: got result, NoState checkRoom
            { blocks: [0], arrows: [{from:5,  fl:1, tl:0}] },                                              //  6  Orch: route to Hotel
            { blocks: [3], arrows: [{from:6,  fl:0, tl:3}] },                                              //  7  Hotel: checkRoom OK
            { blocks: [0], arrows: [{from:7,  fl:3, tl:0}] },                                              //  8  Orch: route back to Travel
            { blocks: [1], arrows: [{from:8,  fl:0, tl:1}] },                                              //  9  Travel: got result, NoState bookTrain
            { blocks: [0], arrows: [{from:9,  fl:1, tl:0}] },                                              // 10  Orch: route to Train
            { blocks: [2], arrows: [{from:10, fl:0, tl:2}] },                                              // 11  Train: bookTrain write OK
            { blocks: [0], arrows: [{from:11, fl:2, tl:0}] },                                              // 12  Orch: route back to Travel
            { blocks: [1], arrows: [{from:12, fl:0, tl:1}] },                                              // 13  Travel: got result, NoState bookHotel
            { blocks: [0], arrows: [{from:13, fl:1, tl:0}] },                                              // 14  Orch: route to Hotel
            { blocks: [3], arrows: [{from:14, fl:0, tl:3}] },                                              // 15  Hotel: bookHotel write OK
            { blocks: [0], arrows: [{from:15, fl:3, tl:0}] },                                              // 16  Orch: route back to Travel
            { blocks: [1], arrows: [{from:16, fl:0, tl:1}] },                                              // 17  Travel: customers[]=true, SUCCESS
            { blocks: [0], arrows: [{from:17, fl:1, tl:0}] },                                              // 18  Orch: broadcast SUCCESS
            { blocks: [1, 2, 3], arrows: [{from:18,fl:0,tl:1},{from:18,fl:0,tl:2},{from:18,fl:0,tl:3}] }  // 19  All: Unlock + Commit
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
            .text('Baseline Protocol');

        this.drawLanes(s, this.blLanes, this.BL_TOTAL);
        this.drawStepNumbers(this.blLanes[0] - 22, this.BL_TOTAL);

        // ── Divider ──
        s.append('line')
            .attr('x1', 20).attr('y1', 300).attr('x2', 1180).attr('y2', 300)
            .attr('stroke', '#ddd').attr('stroke-width', 1);

        // ── 2PC section ──
        s.append('text')
            .attr('x', 20).attr('y', 338)
            .attr('font-size', '15px').attr('font-weight', '700').attr('fill', '#333')
            .text('Matrix');

        this.drawLanes(s, this.tpLanes, this.TP_TOTAL);
        this.drawStepNumbers(this.tpLanes[0] - 22, this.TP_TOTAL);

    }

    drawLanes(s, lanes, numSteps) {
        const endX = this.sx(numSteps) + 35;
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

    drawStepNumbers(y, numSteps) {
        for (let i = 1; i <= numSteps; i++) {
            this.svg.append('text')
                .attr('x', this.sx(i)).attr('y', y)
                .attr('text-anchor', 'middle')
                .attr('font-size', '9px').attr('fill', '#bbb')
                .text(i);
        }
    }

    // ── Dynamic rendering ─────────────────────────────────────────────

    render() {
        this.gBL.selectAll('*').remove();
        this.gTP.selectAll('*').remove();
        this.gStatus.selectAll('*').remove();

        // ── Baseline steps ──
        const blN = Math.min(this.currentStep, this.BL_TOTAL);
        for (let i = 0; i < blN; i++) {
            const isLast = (i + 1 === this.BL_TOTAL);
            this.renderStep(this.gBL, this.blSteps[i], i + 1, this.blLanes, isLast ? this.DONE : null);
        }

        // ── 2PC steps (cycles every 4 steps) ──
        if (this.currentStep > 0) {
            // Which step within the current 4-step cycle (1 to 4)
            const tpCycleStep = ((this.currentStep - 1) % this.TP_TOTAL) + 1;

            // RPC simulation arrows
            this.renderRPC();

            // Render current cycle's steps
            for (let i = 0; i < tpCycleStep; i++) {
                const isLast = (i + 1 === this.TP_TOTAL);
                this.renderStep(this.gTP, this.tpSteps[i], i + 1, this.tpLanes, isLast ? this.DONE : null);
            }
        }

        // ── Commit counters ──
        this.renderCounters();
    }

    renderStep(g, step, num, lanes, fillOverride) {
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
                .attr('fill', fillOverride || this.blockFill(lane)).attr('rx', 3);
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

    renderCounters() {
        // ── Baseline committed count (right-aligned in header line) ──
        const blText = this.gStatus.append('text')
            .attr('x', 1180).attr('y', 38)
            .attr('text-anchor', 'end');

        blText.append('tspan')
            .attr('font-size', '13px').attr('fill', '#888')
            .text('Committed Tx: ');
        blText.append('tspan')
            .attr('font-size', '18px').attr('font-weight', '700')
            .attr('fill', this.blCommitCount > 0 ? this.DONE : '#ccc')
            .text(this.blCommitCount);

        // ── 2PC committed count (right-aligned in header line) ──
        const tpText = this.gStatus.append('text')
            .attr('x', 1180).attr('y', 338)
            .attr('text-anchor', 'end');

        tpText.append('tspan')
            .attr('font-size', '13px').attr('fill', '#888')
            .text('Committed Tx: ');
        tpText.append('tspan')
            .attr('font-size', '18px').attr('font-weight', '700')
            .attr('fill', this.tpCommitCount > 0 ? this.DONE : '#ccc')
            .text(this.tpCommitCount);

    }

    // ── Animation controls ────────────────────────────────────────────

    play() {
        if (this.isPlaying) return;
        this.isPlaying = true;
        this._tick();
    }

    _tick() {
        if (!this.isPlaying) return;

        this.step();

        // Pause longer at end of loop to show completion, then loop
        const delay = this.currentStep >= this.BL_TOTAL ? 2000 : 800;
        this.timer = setTimeout(() => this._tick(), delay / this.speed);
    }

    pause() {
        this.isPlaying = false;
        if (this.timer) { clearTimeout(this.timer); this.timer = null; }
    }

    step() {
        // Loop: when we've reached the end, reset for next cycle
        if (this.currentStep >= this.BL_TOTAL) {
            this.currentStep = 0;
        }
        this.currentStep++;

        // 2PC commits every 4 steps
        if (this.currentStep % this.TP_TOTAL === 0) {
            this.tpCommitCount++;
        }

        // Baseline commits when all 19 steps complete
        if (this.currentStep >= this.BL_TOTAL) {
            this.blCommitCount++;
        }

        this.render();
    }

    reset() {
        this.pause();
        this.currentStep = 0;
        this.blCommitCount = 0;
        this.tpCommitCount = 0;
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
