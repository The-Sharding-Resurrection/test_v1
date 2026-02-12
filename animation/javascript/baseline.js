/**
 * Standalone Baseline Protocol Block Flow Chart
 * 13 block steps with per-step annotations in info panel.
 */

class BaselineVisualization {
    constructor() {
        this.svg = d3.select('#baseline-svg');
        if (this.svg.empty()) return;

        this.info = document.getElementById('baseline-info');
        this.currentStep = 0;
        this.isPlaying = false;
        this.speed = 1.0;
        this.timer = null;

        // Layout
        this.X0 = 150;
        this.DX = 70;
        this.BLK = 28;
        this.H = 14;

        this.lanes = [80, 145, 210, 275];
        this.LABELS = ['Orch Shard', 'Travel (A)', 'Train (B)', 'Hotel (C)'];

        this.ORCH  = '#C0392B';
        this.SHARD = '#1B2631';
        this.ARROW = '#777';
        this.DONE  = '#27ae60';

        this.initData();
        this.initSVG();
        this.bindControls();
    }

    sx(n) { return this.X0 + (n - 1) * this.DX; }
    fill(lane) { return lane === 0 ? this.ORCH : this.SHARD; }

    initData() {
        this.steps = [
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
        this.steps.forEach(s => { if (!s.arrows) s.arrows = []; });

        this.descs = [
            'Step 1 \u2014 Travel(A) executes bookTrainAndHotel() \u2192 NoStateError: Train.checkSeat()',
            'Step 2 \u2014 Orchestrator routes execution to Train(B)',
            'Step 3 \u2014 Train(B) re-executes \u2192 NoStateError: Hotel.checkRoom()',
            'Step 4 \u2014 Orchestrator routes execution to Hotel(C)',
            'Step 5 \u2014 Hotel(C) re-executes \u2192 NoStateError: Train.book()',
            'Step 6 \u2014 Orchestrator routes execution to Train(B)',
            'Step 7 \u2014 Train(B) re-executes \u2192 NoStateError: Hotel.book()',
            'Step 8 \u2014 Orchestrator routes execution to Hotel(C)',
            'Step 9 \u2014 Hotel(C) re-executes \u2192 NoStateError: Agency.customers()',
            'Step 10 \u2014 Orchestrator routes execution to Travel(A)',
            'Step 11 \u2014 Travel(A) re-executes \u2192 SUCCESS (all state available)',
            'Step 12 \u2014 Orchestrator broadcasts SUCCESS to all shards',
            'Step 13 \u2014 All shards: Unlock + Commit (Travel, Train, Hotel)'
        ];

        // Short annotations displayed inside the SVG below the blocks
        this.shortLabels = [
            'NoState:\ncheckSeat',   'Route\n\u2192 Train',
            'NoState:\ncheckRoom',   'Route\n\u2192 Hotel',
            'NoState:\nTrain.book',  'Route\n\u2192 Train',
            'NoState:\nHotel.book',  'Route\n\u2192 Hotel',
            'NoState:\ncustomers',   'Route\n\u2192 Travel',
            'SUCCESS',               'Broadcast',
            'Commit'
        ];
    }

    initSVG() {
        this.svg
            .attr('viewBox', '0 0 1200 480')
            .attr('preserveAspectRatio', 'xMidYMid meet');
        this.svg.selectAll('*').remove();

        this.svg.append('rect').attr('width', 1200).attr('height', 480).attr('fill', '#fff');

        const defs = this.svg.append('defs');
        defs.append('marker')
            .attr('id', 'bl-arr')
            .attr('viewBox', '0 -4 8 8')
            .attr('refX', 7).attr('refY', 0)
            .attr('markerWidth', 7).attr('markerHeight', 7)
            .attr('orient', 'auto')
            .append('path').attr('d', 'M0,-3L7,0L0,3Z').attr('fill', this.ARROW);

        this.drawStatic();
        this.gDyn = this.svg.append('g');
    }

    drawStatic() {
        const s = this.svg;

        s.append('text')
            .attr('x', 20).attr('y', 35)
            .attr('font-size', '15px').attr('font-weight', '700').attr('fill', '#333')
            .text('Baseline Protocol \u2014 Iterative Re-execution (13 block steps)');

        const endX = this.sx(13) + 40;
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

        for (let i = 1; i <= 13; i++) {
            s.append('text')
                .attr('x', this.sx(i)).attr('y', this.lanes[0] - 22)
                .attr('text-anchor', 'middle')
                .attr('font-size', '9px').attr('fill', '#bbb')
                .text(i);
        }

        // Legend
        const g = s.append('g').attr('transform', 'translate(960, 380)');
        g.append('rect').attr('x', -8).attr('y', -10)
            .attr('width', 230).attr('height', 60)
            .attr('fill', '#fafafa').attr('stroke', '#ddd').attr('rx', 3);
        g.append('rect').attr('x', 0).attr('y', 0).attr('width', 12).attr('height', 12).attr('fill', this.ORCH).attr('rx', 2);
        g.append('text').attr('x', 18).attr('y', 10).attr('font-size', '10px').attr('fill', '#555').text('Orchestration Shard Block');
        g.append('rect').attr('x', 0).attr('y', 20).attr('width', 12).attr('height', 12).attr('fill', this.SHARD).attr('rx', 2);
        g.append('text').attr('x', 18).attr('y', 30).attr('font-size', '10px').attr('fill', '#555').text('State Shard Block');
    }

    render() {
        this.gDyn.selectAll('*').remove();

        const n = Math.min(this.currentStep, 13);
        for (let i = 0; i < n; i++) {
            this.renderStep(i);
        }

        // Annotations below lanes
        for (let i = 0; i < n; i++) {
            this.renderAnnotation(i);
        }

        if (this.currentStep >= 13) {
            const x = this.sx(13) + 42;
            const y = (this.lanes[0] + this.lanes[3]) / 2;
            this.gDyn.append('text')
                .attr('x', x).attr('y', y - 6)
                .attr('font-size', '13px').attr('font-weight', '700').attr('fill', this.DONE)
                .text('COMPLETE');
            this.gDyn.append('text')
                .attr('x', x).attr('y', y + 12)
                .attr('font-size', '11px').attr('fill', '#666')
                .text('13 block times');
        }

        // Update info panel
        if (this.info) {
            if (this.currentStep === 0) {
                this.info.innerHTML = '<p>Click <strong>Play</strong> or <strong>Step</strong> to start</p>';
            } else if (this.currentStep <= 13) {
                this.info.innerHTML = '<p><strong>' + this.descs[this.currentStep - 1] + '</strong></p>'
                    + '<p>Block step ' + this.currentStep + ' / 13</p>';
            }
        }
    }

    renderStep(idx) {
        const step = this.steps[idx];
        const num = idx + 1;
        const cx = this.sx(num);

        step.arrows.forEach(a => {
            this.gDyn.append('line')
                .attr('x1', this.sx(a.from) + this.H).attr('y1', this.lanes[a.fl])
                .attr('x2', cx - this.H - 2).attr('y2', this.lanes[a.tl])
                .attr('stroke', this.ARROW).attr('stroke-width', 1.5)
                .attr('marker-end', 'url(#bl-arr)');
        });

        step.blocks.forEach(lane => {
            this.gDyn.append('rect')
                .attr('x', cx - this.H).attr('y', this.lanes[lane] - this.H)
                .attr('width', this.BLK).attr('height', this.BLK)
                .attr('fill', this.fill(lane)).attr('rx', 3);
        });
    }

    renderAnnotation(idx) {
        const num = idx + 1;
        const label = this.shortLabels[idx];
        const cx = this.sx(num);
        const baseY = this.lanes[3] + 40;
        const lines = label.split('\n');

        lines.forEach((line, li) => {
            this.gDyn.append('text')
                .attr('x', cx).attr('y', baseY + li * 12)
                .attr('text-anchor', 'middle')
                .attr('font-size', '8px')
                .attr('fill', idx === this.currentStep - 1 ? '#333' : '#bbb')
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
        on('baseline-play',  () => this.play());
        on('baseline-pause', () => this.pause());
        on('baseline-step',  () => this.step());
        on('baseline-reset', () => this.reset());

        const sp = document.getElementById('baseline-speed');
        if (sp) sp.addEventListener('input', e => { this.speed = parseFloat(e.target.value); });
    }
}

document.addEventListener('DOMContentLoaded', () => { new BaselineVisualization(); });
