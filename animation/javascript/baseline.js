/**
 * Baseline Protocol Interactive Visualization
 * Hop-based iterative execution with NoStateError detection
 */

class BaselineVisualization {
    constructor(svgId, stateDisplayId) {
        this.svg = d3.select(`#${svgId}`);
        this.stateDisplay = d3.select(`#${stateDisplayId}`);
        this.width = 900;
        this.height = 600;

        this.state = 'IDLE';
        this.currentHop = 0;
        this.isPlaying = false;
        this.speed = 1.0;
        this.timeElapsed = 0;

        this.nodes = [];
        this.links = [];
        this.messages = [];

        this.rwset = [];

        this.initNetwork();
        this.initArrowMarker();
        this.updateStateDisplay();
    }

    initArrowMarker() {
        // Define arrow marker for message arrows
        this.svg.append('defs')
            .append('marker')
            .attr('id', 'baseline-arrow')
            .attr('viewBox', '0 -5 10 10')
            .attr('refX', 20)
            .attr('refY', 0)
            .attr('markerWidth', 6)
            .attr('markerHeight', 6)
            .attr('orient', 'auto')
            .append('path')
            .attr('d', 'M0,-5L10,0L0,5')
            .attr('fill', '#a855f7');
    }

    initNetwork() {
        const centerX = this.width / 2;
        const centerY = this.height / 2;
        const radius = 180;

        // Orchestrator at center
        this.nodes.push({
            id: 'orchestrator',
            label: 'Orchestrator',
            x: centerX,
            y: centerY - 50,
            type: 'orchestrator',
            status: 'idle'
        });

        // 8 shards in circular layout (only show A, B, C for clarity)
        const shardPositions = [
            { id: 'shard-a', label: 'Shard A\n(Agency)', angle: -90 },
            { id: 'shard-b', label: 'Shard B\n(Train)', angle: -30 },
            { id: 'shard-c', label: 'Shard C\n(Hotel)', angle: 30 }
        ];

        shardPositions.forEach(shard => {
            const angle = (shard.angle * Math.PI) / 180;
            this.nodes.push({
                id: shard.id,
                label: shard.label,
                x: centerX + radius * Math.cos(angle),
                y: centerY + radius * Math.sin(angle),
                type: 'shard',
                status: 'idle'
            });
        });

        this.render();
    }

    render() {
        // Clear previous render
        this.svg.selectAll('*').remove();
        this.initArrowMarker();

        // Draw links (connections)
        this.svg.selectAll('.link')
            .data(this.links)
            .enter()
            .append('line')
            .attr('class', 'link')
            .attr('x1', d => d.source.x)
            .attr('y1', d => d.source.y)
            .attr('x2', d => d.target.x)
            .attr('y2', d => d.target.y)
            .attr('stroke', '#666')
            .attr('stroke-width', 1)
            .attr('stroke-dasharray', '5,5');

        // Draw nodes
        const nodeGroups = this.svg.selectAll('.node')
            .data(this.nodes)
            .enter()
            .append('g')
            .attr('class', 'node')
            .attr('transform', d => `translate(${d.x}, ${d.y})`);

        nodeGroups.append('circle')
            .attr('class', 'node-circle')
            .attr('r', d => d.type === 'orchestrator' ? 40 : 35)
            .attr('fill', d => this.getNodeColor(d))
            .attr('stroke', '#fff')
            .attr('stroke-width', 2);

        nodeGroups.append('text')
            .attr('class', 'node-label')
            .attr('text-anchor', 'middle')
            .attr('dy', '0.35em')
            .attr('fill', '#fff')
            .attr('font-size', '14px')
            .attr('font-weight', 'bold')
            .each(function(d) {
                const lines = d.label.split('\n');
                const text = d3.select(this);
                lines.forEach((line, i) => {
                    text.append('tspan')
                        .attr('x', 0)
                        .attr('dy', i === 0 ? 0 : '1.2em')
                        .text(line);
                });
            });

        // Draw lock icons if node is locked
        nodeGroups.filter(d => d.locked)
            .append('text')
            .attr('class', 'lock-icon')
            .attr('x', 25)
            .attr('y', -25)
            .attr('font-size', '20px')
            .text('🔒');

        // Draw status badges
        nodeGroups.filter(d => d.statusBadge)
            .append('text')
            .attr('class', 'status-badge')
            .attr('x', 45)
            .attr('y', 5)
            .attr('fill', d => d.statusColor || '#06d6a0')
            .attr('font-weight', 'bold')
            .text(d => d.statusBadge);

        // Draw messages
        this.messages.forEach(msg => {
            this.drawMessage(msg);
        });
    }

    getNodeColor(node) {
        if (node.status === 'executing') return '#ffb703';
        if (node.status === 'locked') return '#fb8500';
        if (node.status === 'success') return '#06d6a0';
        if (node.status === 'simulating') return '#3b82f6';
        return '#52b788'; // idle
    }

    drawMessage(msg) {
        const arrow = this.svg.append('line')
            .attr('class', 'message-arrow')
            .attr('x1', msg.from.x)
            .attr('y1', msg.from.y)
            .attr('x2', msg.to.x)
            .attr('y2', msg.to.y)
            .attr('stroke', msg.color || '#a855f7')
            .attr('stroke-width', 3)
            .attr('marker-end', 'url(#baseline-arrow)');

        const midX = (msg.from.x + msg.to.x) / 2;
        const midY = (msg.from.y + msg.to.y) / 2;

        this.svg.append('text')
            .attr('x', midX)
            .attr('y', midY - 10)
            .attr('text-anchor', 'middle')
            .attr('fill', msg.color || '#a855f7')
            .attr('font-size', '12px')
            .attr('font-weight', 'bold')
            .text(msg.label);
    }

    async play() {
        if (this.isPlaying) return;
        this.isPlaying = true;

        while (this.isPlaying && this.state !== 'COMPLETE') {
            await this.step();
            await this.sleep(1000 / this.speed);
        }
    }

    pause() {
        this.isPlaying = false;
    }

    async step() {
        switch (this.state) {
            case 'IDLE':
                await this.startHop0();
                break;
            case 'HOP_0_EXECUTE':
                await this.hop0NoStateError();
                break;
            case 'HOP_0_FORWARD':
                await this.routeToShardB();
                break;
            case 'HOP_1_EXECUTE':
                await this.hop1NoStateError();
                break;
            case 'HOP_1_FORWARD':
                await this.routeToShardC();
                break;
            case 'HOP_2_EXECUTE':
                await this.hop2Success();
                break;
            case 'HOP_2_NOTIFY':
                await this.finalizeAll();
                break;
            case 'COMPLETE':
                break;
        }
    }

    async startHop0() {
        this.state = 'HOP_0_EXECUTE';
        this.timeElapsed += 1;

        const shardA = this.nodes.find(n => n.id === 'shard-a');
        shardA.status = 'executing';

        this.updateStateDisplay('Hop 0: Shard A executing TravelAgency.bookTrainAndHotel()');
        this.render();
    }

    async hop0NoStateError() {
        this.state = 'HOP_0_FORWARD';
        this.timeElapsed += 2;

        const shardA = this.nodes.find(n => n.id === 'shard-a');
        shardA.status = 'locked';
        shardA.locked = true;
        shardA.statusBadge = 'PENDING';
        shardA.statusColor = '#fb8500';

        this.rwset = ['Agency.slot1', 'Agency.slot2'];

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        this.messages = [{
            from: shardA,
            to: orch,
            label: 'PENDING + RwSet',
            color: '#fb8500'
        }];

        this.updateStateDisplay('Hop 0: NoStateError detected → Forward to Orchestrator');
        this.render();
    }

    async routeToShardB() {
        this.state = 'HOP_1_EXECUTE';
        this.timeElapsed += 2;

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        const shardB = this.nodes.find(n => n.id === 'shard-b');

        this.messages = [{
            from: orch,
            to: shardB,
            label: 'Hop 1: Continue on B',
            color: '#a855f7'
        }];

        shardB.status = 'executing';

        this.updateStateDisplay('Orchestrator routes to Shard B for Hop 1');
        this.render();
    }

    async hop1NoStateError() {
        this.state = 'HOP_1_FORWARD';
        this.timeElapsed += 2;

        const shardB = this.nodes.find(n => n.id === 'shard-b');
        shardB.status = 'locked';
        shardB.locked = true;
        shardB.statusBadge = 'PENDING';
        shardB.statusColor = '#fb8500';

        this.rwset = ['Agency.slot1', 'Agency.slot2', 'Train.seat[5]'];

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        this.messages = [{
            from: shardB,
            to: orch,
            label: 'PENDING + RwSet',
            color: '#fb8500'
        }];

        this.updateStateDisplay('Hop 1: NoStateError → Forward updated RwSet to Orchestrator');
        this.render();
    }

    async routeToShardC() {
        this.state = 'HOP_2_EXECUTE';
        this.timeElapsed += 2;

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        const shardC = this.nodes.find(n => n.id === 'shard-c');

        this.messages = [{
            from: orch,
            to: shardC,
            label: 'Hop 2: Continue on C',
            color: '#a855f7'
        }];

        shardC.status = 'executing';

        this.updateStateDisplay('Orchestrator routes to Shard C for Hop 2');
        this.render();
    }

    async hop2Success() {
        this.state = 'HOP_2_NOTIFY';
        this.timeElapsed += 2;

        const shardC = this.nodes.find(n => n.id === 'shard-c');
        shardC.status = 'success';
        shardC.locked = true;
        shardC.statusBadge = 'SUCCESS';
        shardC.statusColor = '#06d6a0';

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        this.messages = [{
            from: shardC,
            to: orch,
            label: 'SUCCESS',
            color: '#06d6a0'
        }];

        this.updateStateDisplay('Hop 2: Complete! Notify Orchestrator');
        this.render();
    }

    async finalizeAll() {
        this.state = 'COMPLETE';
        this.timeElapsed += 1;

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        const shardA = this.nodes.find(n => n.id === 'shard-a');
        const shardB = this.nodes.find(n => n.id === 'shard-b');
        const shardC = this.nodes.find(n => n.id === 'shard-c');

        this.messages = [
            { from: orch, to: shardA, label: 'SUCCESS', color: '#06d6a0' },
            { from: orch, to: shardB, label: 'SUCCESS', color: '#06d6a0' },
            { from: orch, to: shardC, label: 'SUCCESS', color: '#06d6a0' }
        ];

        shardA.status = 'success';
        shardA.locked = false;
        shardA.statusBadge = null;

        shardB.status = 'success';
        shardB.locked = false;
        shardB.statusBadge = null;

        shardC.locked = false;

        this.updateStateDisplay(`COMPLETE! Total time: ${this.timeElapsed}s, 3 hops`);
        this.render();
    }

    reset() {
        this.state = 'IDLE';
        this.currentHop = 0;
        this.isPlaying = false;
        this.timeElapsed = 0;
        this.messages = [];
        this.rwset = [];

        this.nodes.forEach(node => {
            node.status = 'idle';
            node.locked = false;
            node.statusBadge = null;
        });

        this.updateStateDisplay('Click Play to start');
        this.render();
    }

    updateStateDisplay(message) {
        this.stateDisplay.html(`
            <p><strong>State:</strong> ${this.state}</p>
            <p><strong>Time:</strong> ${this.timeElapsed}s</p>
            <p><strong>Message:</strong> ${message}</p>
            <p><strong>RwSet:</strong> ${this.rwset.length > 0 ? this.rwset.join(', ') : 'Empty'}</p>
        `);
    }

    sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }
}

// Initialize Baseline visualization
let baselineViz = null;

document.addEventListener('DOMContentLoaded', () => {
    baselineViz = new BaselineVisualization('baseline-svg', 'baseline-state');

    document.getElementById('baseline-play').addEventListener('click', () => {
        baselineViz.play();
    });

    document.getElementById('baseline-pause').addEventListener('click', () => {
        baselineViz.pause();
    });

    document.getElementById('baseline-step').addEventListener('click', () => {
        baselineViz.step();
    });

    document.getElementById('baseline-reset').addEventListener('click', () => {
        baselineViz.reset();
    });

    document.getElementById('baseline-speed').addEventListener('input', (e) => {
        baselineViz.speed = parseFloat(e.target.value);
    });
});
