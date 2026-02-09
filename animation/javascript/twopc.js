/**
 * 2PC Protocol Interactive Visualization
 * Simulation-based two-phase commit with atomic guarantees
 */

class TwoPCVisualization {
    constructor(svgId, stateDisplayId) {
        this.svg = d3.select(`#${svgId}`);
        this.stateDisplay = d3.select(`#${stateDisplayId}`);
        this.width = 900;
        this.height = 600;

        this.state = 'IDLE';
        this.isPlaying = false;
        this.speed = 1.0;
        this.timeElapsed = 0;

        this.nodes = [];
        this.links = [];
        this.messages = [];

        this.rwset = [];
        this.votes = {};

        this.initNetwork();
        this.initArrowMarker();
        this.updateStateDisplay();
    }

    initArrowMarker() {
        this.svg.append('defs')
            .append('marker')
            .attr('id', 'twopc-arrow')
            .attr('viewBox', '0 -5 10 10')
            .attr('refX', 20)
            .attr('refY', 0)
            .attr('markerWidth', 6)
            .attr('markerHeight', 6)
            .attr('orient', 'auto')
            .append('path')
            .attr('d', 'M0,-5L10,0L0,5')
            .attr('fill', '#3b82f6');
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

        // 3 shards (A, B, C)
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
        this.svg.selectAll('*').remove();
        this.initArrowMarker();

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

        // Draw lock icons
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
        if (node.status === 'simulating') return '#3b82f6';
        if (node.status === 'locked') return '#fb8500';
        if (node.status === 'success') return '#06d6a0';
        return '#52b788'; // idle
    }

    drawMessage(msg) {
        const arrow = this.svg.append('line')
            .attr('class', 'message-arrow')
            .attr('x1', msg.from.x)
            .attr('y1', msg.from.y)
            .attr('x2', msg.to.x)
            .attr('y2', msg.to.y)
            .attr('stroke', msg.color || '#3b82f6')
            .attr('stroke-width', 3)
            .attr('marker-end', 'url(#twopc-arrow)');

        const midX = (msg.from.x + msg.to.x) / 2;
        const midY = (msg.from.y + msg.to.y) / 2;

        this.svg.append('text')
            .attr('x', midX)
            .attr('y', midY - 10)
            .attr('text-anchor', 'middle')
            .attr('fill', msg.color || '#3b82f6')
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
                await this.startSubmit();
                break;
            case 'SUBMIT':
                await this.startSimulation();
                break;
            case 'SIMULATION':
                await this.finishSimulation();
                break;
            case 'PREPARE_BROADCAST':
                await this.shardsValidate();
                break;
            case 'PREPARE_VOTE':
                await this.collectVotes();
                break;
            case 'COMMIT_BROADCAST':
                await this.atomicFinalize();
                break;
            case 'COMPLETE':
                break;
        }
    }

    async startSubmit() {
        this.state = 'SUBMIT';
        this.timeElapsed += 1;

        const shardA = this.nodes.find(n => n.id === 'shard-a');
        const orch = this.nodes.find(n => n.id === 'orchestrator');

        this.messages = [{
            from: shardA,
            to: orch,
            label: 'Cross-shard tx',
            color: '#fb8500'
        }];

        this.updateStateDisplay('User submits to Shard A → Forwarded to Orchestrator');
        this.render();
    }

    async startSimulation() {
        this.state = 'SIMULATION';
        this.timeElapsed += 1;

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        orch.status = 'simulating';

        const shardA = this.nodes.find(n => n.id === 'shard-a');
        const shardB = this.nodes.find(n => n.id === 'shard-b');
        const shardC = this.nodes.find(n => n.id === 'shard-c');

        this.messages = [
            { from: orch, to: shardA, label: 'Fetch state', color: '#3b82f6' },
            { from: orch, to: shardB, label: 'Fetch state', color: '#3b82f6' },
            { from: orch, to: shardC, label: 'Fetch state', color: '#3b82f6' }
        ];

        this.updateStateDisplay('Orchestrator: Running EVM simulation, fetching state');
        this.render();
    }

    async finishSimulation() {
        this.state = 'PREPARE_BROADCAST';
        this.timeElapsed += 2;

        this.rwset = [
            'Agency.slot1 (A)',
            'Agency.slot2 (A)',
            'Train.seat[5] (B)',
            'Hotel.room[3] (C)'
        ];

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        const shardA = this.nodes.find(n => n.id === 'shard-a');
        const shardB = this.nodes.find(n => n.id === 'shard-b');
        const shardC = this.nodes.find(n => n.id === 'shard-c');

        this.messages = [
            { from: orch, to: shardA, label: 'PREPARE', color: '#fb8500' },
            { from: orch, to: shardB, label: 'PREPARE', color: '#fb8500' },
            { from: orch, to: shardC, label: 'PREPARE', color: '#fb8500' }
        ];

        this.updateStateDisplay('Simulation complete! RwSet discovered. Broadcasting PREPARE');
        this.render();
    }

    async shardsValidate() {
        this.state = 'PREPARE_VOTE';
        this.timeElapsed += 2;

        const shardA = this.nodes.find(n => n.id === 'shard-a');
        const shardB = this.nodes.find(n => n.id === 'shard-b');
        const shardC = this.nodes.find(n => n.id === 'shard-c');

        shardA.status = 'locked';
        shardA.locked = true;
        shardA.statusBadge = 'YES';
        shardA.statusColor = '#06d6a0';

        shardB.status = 'locked';
        shardB.locked = true;
        shardB.statusBadge = 'YES';
        shardB.statusColor = '#06d6a0';

        shardC.status = 'locked';
        shardC.locked = true;
        shardC.statusBadge = 'YES';
        shardC.statusColor = '#06d6a0';

        this.votes = { 'shard-a': true, 'shard-b': true, 'shard-c': true };

        this.messages = [];

        this.updateStateDisplay('Shards: Validate RwSet, lock state, vote YES');
        this.render();
    }

    async collectVotes() {
        this.state = 'COMMIT_BROADCAST';
        this.timeElapsed += 1;

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        const shardA = this.nodes.find(n => n.id === 'shard-a');
        const shardB = this.nodes.find(n => n.id === 'shard-b');
        const shardC = this.nodes.find(n => n.id === 'shard-c');

        this.messages = [
            { from: shardA, to: orch, label: 'Vote: YES', color: '#06d6a0' },
            { from: shardB, to: orch, label: 'Vote: YES', color: '#06d6a0' },
            { from: shardC, to: orch, label: 'Vote: YES', color: '#06d6a0' }
        ];

        this.updateStateDisplay('Orchestrator: Collect votes (3/3 YES) → Decision: COMMIT');
        this.render();
    }

    async atomicFinalize() {
        this.state = 'COMPLETE';
        this.timeElapsed += 1;

        const orch = this.nodes.find(n => n.id === 'orchestrator');
        const shardA = this.nodes.find(n => n.id === 'shard-a');
        const shardB = this.nodes.find(n => n.id === 'shard-b');
        const shardC = this.nodes.find(n => n.id === 'shard-c');

        this.messages = [
            { from: orch, to: shardA, label: 'COMMIT', color: '#06d6a0' },
            { from: orch, to: shardB, label: 'COMMIT', color: '#06d6a0' },
            { from: orch, to: shardC, label: 'COMMIT', color: '#06d6a0' }
        ];

        shardA.status = 'success';
        shardA.locked = false;
        shardA.statusBadge = null;

        shardB.status = 'success';
        shardB.locked = false;
        shardB.statusBadge = null;

        shardC.status = 'success';
        shardC.locked = false;
        shardC.statusBadge = null;

        orch.status = 'idle';

        this.updateStateDisplay(`COMPLETE! Total time: ${this.timeElapsed}s, 2 rounds (Prepare + Commit)`);
        this.render();
    }

    reset() {
        this.state = 'IDLE';
        this.isPlaying = false;
        this.timeElapsed = 0;
        this.messages = [];
        this.rwset = [];
        this.votes = {};

        this.nodes.forEach(node => {
            node.status = 'idle';
            node.locked = false;
            node.statusBadge = null;
        });

        this.updateStateDisplay('Click Play to start');
        this.render();
    }

    updateStateDisplay(message) {
        const voteCount = Object.keys(this.votes).length;
        this.stateDisplay.html(`
            <p><strong>State:</strong> ${this.state}</p>
            <p><strong>Time:</strong> ${this.timeElapsed}s</p>
            <p><strong>Message:</strong> ${message}</p>
            <p><strong>RwSet:</strong> ${this.rwset.length > 0 ? this.rwset.length + ' entries' : 'Empty'}</p>
            <p><strong>Votes:</strong> ${voteCount}/3 collected</p>
        `);
    }

    sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }
}

// Initialize 2PC visualization
let twoPCViz = null;

document.addEventListener('DOMContentLoaded', () => {
    twoPCViz = new TwoPCVisualization('twopc-svg', 'twopc-state');

    document.getElementById('twopc-play').addEventListener('click', () => {
        twoPCViz.play();
    });

    document.getElementById('twopc-pause').addEventListener('click', () => {
        twoPCViz.pause();
    });

    document.getElementById('twopc-step').addEventListener('click', () => {
        twoPCViz.step();
    });

    document.getElementById('twopc-reset').addEventListener('click', () => {
        twoPCViz.reset();
    });

    document.getElementById('twopc-speed').addEventListener('input', (e) => {
        twoPCViz.speed = parseFloat(e.target.value);
    });
});
