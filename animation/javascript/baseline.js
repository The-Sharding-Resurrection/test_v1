/**
 * Standalone Baseline Protocol Block Flow Chart
 * 19 block steps with per-step annotations in info panel.
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
        this.TOTAL = 19;

        // Layout
        this.X0 = 130;
        this.DX = 55;
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
        // Lanes: 0=Orch, 1=Travel(A), 2=Train(B), 3=Hotel(C)
        this.steps = [
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
        this.steps.forEach(s => { if (!s.arrows) s.arrows = []; });

        this.descs = [
            'Step 1 \u2014 Travel(A) executes bookTrainAndHotel() \u2192 NoStateError: Train.checkSeat()',
            'Step 2 \u2014 Orchestrator routes execution to Train(B)',
            'Step 3 \u2014 Train(B) re-executes \u2192 checkSeatAvailability() OK',
            'Step 4 \u2014 Orchestrator routes result back to Travel(A)',
            'Step 5 \u2014 Travel(A) got train availability, continues \u2192 NoStateError: Hotel.checkRoom()',
            'Step 6 \u2014 Orchestrator routes execution to Hotel(C)',
            'Step 7 \u2014 Hotel(C) re-executes \u2192 checkRoomAvailability() OK',
            'Step 8 \u2014 Orchestrator routes result back to Travel(A)',
            'Step 9 \u2014 Travel(A) got hotel availability, continues \u2192 NoStateError: Train.bookTrain()',
            'Step 10 \u2014 Orchestrator routes execution to Train(B)',
            'Step 11 \u2014 Train(B) re-executes \u2192 bookTrain() write OK',
            'Step 12 \u2014 Orchestrator routes result back to Travel(A)',
            'Step 13 \u2014 Travel(A) got booking result, continues \u2192 NoStateError: Hotel.bookHotel()',
            'Step 14 \u2014 Orchestrator routes execution to Hotel(C)',
            'Step 15 \u2014 Hotel(C) re-executes \u2192 bookHotel() write OK',
            'Step 16 \u2014 Orchestrator routes result back to Travel(A)',
            'Step 17 \u2014 Travel(A) re-executes \u2192 customers[msg.sender] = true \u2192 SUCCESS',
            'Step 18 \u2014 Orchestrator broadcasts SUCCESS to all shards',
            'Step 19 \u2014 All shards: Unlock + Commit (Travel, Train, Hotel)'
        ];

        this.shortLabels = [
            'NoState:\ncheckSeat',     'Route\n\u2192 Train',
            'checkSeat\nOK',           'Route\n\u2192 Travel',
            'NoState:\ncheckRoom',     'Route\n\u2192 Hotel',
            'checkRoom\nOK',           'Route\n\u2192 Travel',
            'NoState:\nbookTrain',     'Route\n\u2192 Train',
            'bookTrain\nOK',           'Route\n\u2192 Travel',
            'NoState:\nbookHotel',     'Route\n\u2192 Hotel',
            'bookHotel\nOK',           'Route\n\u2192 Travel',
            'SUCCESS',                 'Broadcast',
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
            .text('Baseline Protocol');

        const endX = this.sx(this.TOTAL) + 40;
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

        for (let i = 1; i <= this.TOTAL; i++) {
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

        const n = Math.min(this.currentStep, this.TOTAL);
        for (let i = 0; i < n; i++) {
            this.renderStep(i);
        }

        // Annotations below lanes
        for (let i = 0; i < n; i++) {
            this.renderAnnotation(i);
        }

        if (this.currentStep >= this.TOTAL) {
            const x = this.sx(this.TOTAL) + 42;
            const y = (this.lanes[0] + this.lanes[3]) / 2;
            this.gDyn.append('text')
                .attr('x', x).attr('y', y - 6)
                .attr('font-size', '13px').attr('font-weight', '700').attr('fill', this.DONE)
                .text('COMPLETE');
            this.gDyn.append('text')
                .attr('x', x).attr('y', y + 12)
                .attr('font-size', '11px').attr('fill', '#666')
                .text('19 block times');
        }

        // Update info panel
        if (this.info) {
            if (this.currentStep === 0) {
                this.info.innerHTML = '<p>Click <strong>Play</strong> or <strong>Step</strong> to start</p>';
            } else if (this.currentStep <= this.TOTAL) {
                this.info.innerHTML = '<p><strong>' + this.descs[this.currentStep - 1] + '</strong></p>'
                    + '<p>Block step ' + this.currentStep + ' / ' + this.TOTAL + '</p>';
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
        if (!this.isPlaying || this.currentStep >= this.TOTAL) {
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
        if (this.currentStep >= this.TOTAL) return;
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
