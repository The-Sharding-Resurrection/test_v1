/**
 * Standalone 2PC (Matrix) Protocol Block Flow Chart
 * 4 block steps with pre-simulation RPC phase and per-step annotations.
 */

class TwoPCVisualization {
    constructor() {
        this.svg = d3.select('#twopc-svg');
        if (this.svg.empty()) return;

        this.info = document.getElementById('twopc-info');
        this.currentStep = 0;   // 0 = nothing, 1 = sim shown, 2-5 = block steps 1-4
        this.isPlaying = false;
        this.speed = 1.0;
        this.timer = null;

        // Layout — wider spacing since only 4 block steps
        this.X0 = 260;
        this.DX = 140;
        this.BLK = 28;
        this.H = 14;

        this.lanes = [80, 145, 210, 275];
        this.LABELS = ['Orch Shard', 'Travel (A)', 'Train (B)', 'Hotel (C)'];

        this.ORCH  = '#C0392B';
        this.SHARD = '#1B2631';
        this.ARROW = '#777';
        this.RPC   = '#aaa';
        this.DONE  = '#27ae60';

        this.initData();
        this.initSVG();
        this.bindControls();
    }

    sx(n) { return this.X0 + (n - 1) * this.DX; }
    fill(lane) { return lane === 0 ? this.ORCH : this.SHARD; }

    initData() {
        this.steps = [
            { blocks: [0], arrows: [] },                                                                  //  1
            { blocks: [1, 2, 3], arrows: [{from:1,fl:0,tl:1},{from:1,fl:0,tl:2},{from:1,fl:0,tl:3}] },  //  2
            { blocks: [0], arrows: [{from:2,fl:1,tl:0},{from:2,fl:2,tl:0},{from:2,fl:3,tl:0}] },         //  3
            { blocks: [1, 2, 3], arrows: [{from:3,fl:0,tl:1},{from:3,fl:0,tl:2},{from:3,fl:0,tl:3}] }    //  4
        ];

        // Step 0 = sim phase, steps 1-4 = block steps
        this.descs = [
            'Pre-simulation \u2014 Orchestrator fetches state from all shards via HTTP RPC',
            'Step 1 \u2014 Orchestrator broadcasts CtToOrder (PREPARE) to all involved shards',
            'Step 2 \u2014 All shards: Lock state + Vote YES (Travel, Train, Hotel simultaneously)',
            'Step 3 \u2014 Orchestrator collects votes \u2192 TpcResult: COMMIT',
            'Step 4 \u2014 All shards: Finalize \u2014 unlock + apply state changes'
        ];

        this.shortLabels = [
            'PREPARE\nbroadcast',
            'Lock +\nVote YES',
            'TpcResult:\nCOMMIT',
            'Finalize\n(unlock + apply)'
        ];
    }

    initSVG() {
        this.svg
            .attr('viewBox', '0 0 1000 480')
            .attr('preserveAspectRatio', 'xMidYMid meet');
        this.svg.selectAll('*').remove();

        this.svg.append('rect').attr('width', 1000).attr('height', 480).attr('fill', '#fff');

        const defs = this.svg.append('defs');
        // Solid arrow
        defs.append('marker')
            .attr('id', 'tp-arr')
            .attr('viewBox', '0 -4 8 8')
            .attr('refX', 7).attr('refY', 0)
            .attr('markerWidth', 7).attr('markerHeight', 7)
            .attr('orient', 'auto')
            .append('path').attr('d', 'M0,-3L7,0L0,3Z').attr('fill', this.ARROW);
        // Dashed arrow
        defs.append('marker')
            .attr('id', 'tp-dash')
            .attr('viewBox', '0 -4 8 8')
            .attr('refX', 7).attr('refY', 0)
            .attr('markerWidth', 7).attr('markerHeight', 7)
            .attr('orient', 'auto')
            .append('path').attr('d', 'M0,-3L7,0L0,3Z').attr('fill', this.RPC);

        this.drawStatic();
        this.gDyn = this.svg.append('g');
    }

    drawStatic() {
        const s = this.svg;

        s.append('text')
            .attr('x', 20).attr('y', 35)
            .attr('font-size', '15px').attr('font-weight', '700').attr('fill', '#333')
            .text('2PC Protocol (Matrix) \u2014 Simulation + 2-Phase Commit (4 block steps)');

        const endX = this.sx(4) + 50;
        this.lanes.forEach((y, i) => {
            s.append('text')
                .attr('x', 100).attr('y', y + 5)
                .attr('text-anchor', 'end')
                .attr('font-size', '12px').attr('fill', '#555')
                .text(this.LABELS[i]);
            s.append('line')
                .attr('x1', 110).attr('y1', y)
                .attr('x2', endX).attr('y2', y)
                .attr('stroke', '#eee').attr('stroke-width', 1)
                .attr('stroke-dasharray', '4,4');
        });

        // Step column numbers
        for (let i = 1; i <= 4; i++) {
            s.append('text')
                .attr('x', this.sx(i)).attr('y', this.lanes[0] - 22)
                .attr('text-anchor', 'middle')
                .attr('font-size', '10px').attr('fill', '#bbb')
                .text(i);
        }

        // "Pre" label for simulation column
        s.append('text')
            .attr('x', this.X0 - 80).attr('y', this.lanes[0] - 22)
            .attr('text-anchor', 'middle')
            .attr('font-size', '10px').attr('fill', '#bbb')
            .text('pre');

        // Legend
        const g = s.append('g').attr('transform', 'translate(740, 370)');
        g.append('rect').attr('x', -8).attr('y', -10)
            .attr('width', 240).attr('height', 80)
            .attr('fill', '#fafafa').attr('stroke', '#ddd').attr('rx', 3);
        g.append('rect').attr('x', 0).attr('y', 0).attr('width', 12).attr('height', 12).attr('fill', this.ORCH).attr('rx', 2);
        g.append('text').attr('x', 18).attr('y', 10).attr('font-size', '10px').attr('fill', '#555').text('Orchestration Shard Block');
        g.append('rect').attr('x', 0).attr('y', 20).attr('width', 12).attr('height', 12).attr('fill', this.SHARD).attr('rx', 2);
        g.append('text').attr('x', 18).attr('y', 30).attr('font-size', '10px').attr('fill', '#555').text('State Shard Block');
        g.append('line').attr('x1', 0).attr('y1', 48).attr('x2', 12).attr('y2', 48).attr('stroke', this.ARROW).attr('stroke-width', 1.5);
        g.append('text').attr('x', 18).attr('y', 51).attr('font-size', '10px').attr('fill', '#555').text('Block propagation');
        g.append('line').attr('x1', 120).attr('y1', 48).attr('x2', 132).attr('y2', 48).attr('stroke', this.RPC).attr('stroke-width', 1.5).attr('stroke-dasharray', '4,3');
        g.append('text').attr('x', 138).attr('y', 51).attr('font-size', '10px').attr('fill', '#555').text('RPC (HTTP)');
    }

    render() {
        this.gDyn.selectAll('*').remove();

        // Pre-simulation RPC arrows (shown from step >= 1)
        if (this.currentStep >= 1) {
            this.renderRPC();
        }

        // Block steps (shown from step >= 2; step 2 = block 1, step 5 = block 4)
        const blockN = Math.min(Math.max(this.currentStep - 1, 0), 4);
        for (let i = 0; i < blockN; i++) {
            this.renderBlockStep(i);
        }

        // Annotations
        for (let i = 0; i < blockN; i++) {
            this.renderAnnotation(i, i === blockN - 1);
        }

        // Completion
        if (this.currentStep >= 5) {
            const x = this.sx(4) + 50;
            const y = (this.lanes[0] + this.lanes[3]) / 2;
            this.gDyn.append('text')
                .attr('x', x).attr('y', y - 6)
                .attr('font-size', '14px').attr('font-weight', '700').attr('fill', this.DONE)
                .text('COMPLETE');
            this.gDyn.append('text')
                .attr('x', x).attr('y', y + 14)
                .attr('font-size', '12px').attr('fill', '#666')
                .text('4 block times');
        }

        // Info panel
        if (this.info) {
            if (this.currentStep === 0) {
                this.info.innerHTML = '<p>Click <strong>Play</strong> or <strong>Step</strong> to start</p>';
            } else {
                const descIdx = Math.min(this.currentStep - 1, this.descs.length - 1);
                this.info.innerHTML = '<p><strong>' + this.descs[descIdx] + '</strong></p>'
                    + '<p>Phase ' + this.currentStep + ' / 5 (1 sim + 4 block steps)</p>';
            }
        }
    }

    renderRPC() {
        const ox = this.X0 - 80;
        const oy = this.lanes[0];

        [1, 2, 3].forEach((lane, i) => {
            const dx = ox + (i - 1) * 15;
            const sy = this.lanes[lane];

            // Down arrow (state fetch)
            this.gDyn.append('line')
                .attr('x1', dx).attr('y1', oy + 8)
                .attr('x2', dx).attr('y2', sy - 8)
                .attr('stroke', this.RPC).attr('stroke-width', 1)
                .attr('stroke-dasharray', '4,3')
                .attr('marker-end', 'url(#tp-dash)');

            // Return arrow (slightly offset)
            this.gDyn.append('line')
                .attr('x1', dx + 6).attr('y1', sy - 8)
                .attr('x2', dx + 6).attr('y2', oy + 8)
                .attr('stroke', this.RPC).attr('stroke-width', 1)
                .attr('stroke-dasharray', '4,3')
                .attr('marker-end', 'url(#tp-dash)');
        });

        // Label
        this.gDyn.append('text')
            .attr('x', ox).attr('y', oy - 10)
            .attr('text-anchor', 'middle')
            .attr('font-size', '10px').attr('fill', '#aaa')
            .attr('font-style', 'italic')
            .text('HTTP simulation');
    }

    renderBlockStep(idx) {
        const step = this.steps[idx];
        const num = idx + 1;
        const cx = this.sx(num);

        step.arrows.forEach(a => {
            this.gDyn.append('line')
                .attr('x1', this.sx(a.from) + this.H).attr('y1', this.lanes[a.fl])
                .attr('x2', cx - this.H - 2).attr('y2', this.lanes[a.tl])
                .attr('stroke', this.ARROW).attr('stroke-width', 1.5)
                .attr('marker-end', 'url(#tp-arr)');
        });

        step.blocks.forEach(lane => {
            this.gDyn.append('rect')
                .attr('x', cx - this.H).attr('y', this.lanes[lane] - this.H)
                .attr('width', this.BLK).attr('height', this.BLK)
                .attr('fill', this.fill(lane)).attr('rx', 3);
        });
    }

    renderAnnotation(idx, isCurrent) {
        const cx = this.sx(idx + 1);
        const baseY = this.lanes[3] + 40;
        const lines = this.shortLabels[idx].split('\n');

        lines.forEach((line, li) => {
            this.gDyn.append('text')
                .attr('x', cx).attr('y', baseY + li * 13)
                .attr('text-anchor', 'middle')
                .attr('font-size', '9px')
                .attr('fill', isCurrent ? '#333' : '#bbb')
                .text(line);
        });
    }

    // ── Controls ──────────────────────────────────────────────────────

    play() {
        if (this.isPlaying) return;
        this.isPlaying = true;
        this._tick();
    }

    _tick() {
        if (!this.isPlaying || this.currentStep >= 5) {
            this.isPlaying = false;
            return;
        }
        this.advance();
        this.timer = setTimeout(() => this._tick(), 800 / this.speed);
    }

    pause() {
        this.isPlaying = false;
        if (this.timer) { clearTimeout(this.timer); this.timer = null; }
    }

    advance() {
        if (this.currentStep >= 5) return;
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
        on('twopc-play',  () => this.play());
        on('twopc-pause', () => this.pause());
        on('twopc-step',  () => this.advance());
        on('twopc-reset', () => this.reset());

        const sp = document.getElementById('twopc-speed');
        if (sp) sp.addEventListener('input', e => { this.speed = parseFloat(e.target.value); });
    }
}

document.addEventListener('DOMContentLoaded', () => { new TwoPCVisualization(); });
