/**
 * Side-by-Side Comparison Visualization
 * Synchronized playback of Baseline vs 2PC protocols
 */

class ComparisonVisualization {
    constructor() {
        this.leftSvg = d3.select('#comparison-left-svg');
        this.rightSvg = d3.select('#comparison-right-svg');
        this.leftTime = d3.select('#comparison-left-time');
        this.rightTime = d3.select('#comparison-right-time');

        this.width = 440;
        this.height = 500;

        this.isPlaying = false;
        this.speed = 1.0;

        this.leftTimeElapsed = 0;
        this.rightTimeElapsed = 0;

        this.leftNodes = [];
        this.rightNodes = [];

        this.leftMessages = [];
        this.rightMessages = [];

        this.phase = 0;

        this.initNetworks();
        this.initArrowMarkers();
        this.render();
    }

    initArrowMarkers() {
        // Left arrow (Baseline - yellow)
        this.leftSvg.append('defs')
            .append('marker')
            .attr('id', 'comp-left-arrow')
            .attr('viewBox', '0 -5 10 10')
            .attr('refX', 15)
            .attr('refY', 0)
            .attr('markerWidth', 6)
            .attr('markerHeight', 6)
            .attr('orient', 'auto')
            .append('path')
            .attr('d', 'M0,-5L10,0L0,5')
            .attr('fill', '#ffb703');

        // Right arrow (2PC - blue)
        this.rightSvg.append('defs')
            .append('marker')
            .attr('id', 'comp-right-arrow')
            .attr('viewBox', '0 -5 10 10')
            .attr('refX', 15)
            .attr('refY', 0)
            .attr('markerWidth', 6)
            .attr('markerHeight', 6)
            .attr('orient', 'auto')
            .append('path')
            .attr('d', 'M0,-5L10,0L0,5')
            .attr('fill', '#3b82f6');
    }

    initNetworks() {
        const centerX = this.width / 2;
        const centerY = this.height / 2;
        const radius = 120;

        // Create nodes for both sides
        const createNodes = () => {
            const nodes = [];

            // Orchestrator
            nodes.push({
                id: 'orch',
                label: 'Orch',
                x: centerX,
                y: centerY - 80,
                type: 'orchestrator',
                status: 'idle'
            });

            // 3 shards
            const positions = [
                { id: 'a', label: 'A', angle: -90 },
                { id: 'b', label: 'B', angle: -30 },
                { id: 'c', label: 'C', angle: 30 }
            ];

            positions.forEach(pos => {
                const angle = (pos.angle * Math.PI) / 180;
                nodes.push({
                    id: pos.id,
                    label: pos.label,
                    x: centerX + radius * Math.cos(angle),
                    y: centerY + radius * Math.sin(angle) + 20,
                    type: 'shard',
                    status: 'idle'
                });
            });

            return nodes;
        };

        this.leftNodes = createNodes();
        this.rightNodes = createNodes();
    }

    render() {
        this.renderSide(this.leftSvg, this.leftNodes, this.leftMessages, '#ffb703', 'comp-left-arrow');
        this.renderSide(this.rightSvg, this.rightNodes, this.rightMessages, '#3b82f6', 'comp-right-arrow');
    }

    renderSide(svg, nodes, messages, color, arrowId) {
        svg.selectAll('.node').remove();
        svg.selectAll('.message-arrow').remove();
        svg.selectAll('.message-label').remove();

        // Draw nodes
        const nodeGroups = svg.selectAll('.node')
            .data(nodes)
            .enter()
            .append('g')
            .attr('class', 'node')
            .attr('transform', d => `translate(${d.x}, ${d.y})`);

        nodeGroups.append('circle')
            .attr('r', d => d.type === 'orchestrator' ? 30 : 25)
            .attr('fill', d => this.getNodeColor(d))
            .attr('stroke', '#fff')
            .attr('stroke-width', 2);

        nodeGroups.append('text')
            .attr('text-anchor', 'middle')
            .attr('dy', '0.35em')
            .attr('fill', '#fff')
            .attr('font-size', '14px')
            .attr('font-weight', 'bold')
            .text(d => d.label);

        // Draw locks
        nodeGroups.filter(d => d.locked)
            .append('text')
            .attr('x', 18)
            .attr('y', -18)
            .attr('font-size', '16px')
            .text('🔒');

        // Draw status badges
        nodeGroups.filter(d => d.statusBadge)
            .append('text')
            .attr('x', 30)
            .attr('y', 5)
            .attr('fill', d => d.statusColor || '#06d6a0')
            .attr('font-size', '10px')
            .attr('font-weight', 'bold')
            .text(d => d.statusBadge);

        // Draw messages
        messages.forEach(msg => {
            svg.append('line')
                .attr('class', 'message-arrow')
                .attr('x1', msg.from.x)
                .attr('y1', msg.from.y)
                .attr('x2', msg.to.x)
                .attr('y2', msg.to.y)
                .attr('stroke', msg.color || color)
                .attr('stroke-width', 2)
                .attr('marker-end', `url(#${arrowId})`);

            const midX = (msg.from.x + msg.to.x) / 2;
            const midY = (msg.from.y + msg.to.y) / 2;

            svg.append('text')
                .attr('class', 'message-label')
                .attr('x', midX)
                .attr('y', midY - 5)
                .attr('text-anchor', 'middle')
                .attr('fill', msg.color || color)
                .attr('font-size', '10px')
                .attr('font-weight', 'bold')
                .text(msg.label);
        });
    }

    getNodeColor(node) {
        if (node.status === 'executing') return '#ffb703';
        if (node.status === 'simulating') return '#3b82f6';
        if (node.status === 'locked') return '#fb8500';
        if (node.status === 'success') return '#06d6a0';
        return '#52b788';
    }

    async play() {
        if (this.isPlaying) return;
        this.isPlaying = true;

        while (this.isPlaying && this.phase <= 8) {
            await this.step();
            await this.sleep(1500 / this.speed);
        }
    }

    pause() {
        this.isPlaying = false;
    }

    async step() {
        this.phase++;

        switch (this.phase) {
            case 1:
                await this.phase1Initial();
                break;
            case 2:
                await this.phase2BaselineHop0();
                break;
            case 3:
                await this.phase3TwoPCSimulation();
                break;
            case 4:
                await this.phase4BaselineHop1();
                break;
            case 5:
                await this.phase5TwoPCPrepare();
                break;
            case 6:
                await this.phase6BaselineHop2();
                break;
            case 7:
                await this.phase7TwoPCCommit();
                break;
            case 8:
                await this.phase8BaselineFinalize();
                break;
        }

        this.render();
    }

    async phase1Initial() {
        // Both: User submits
        const leftA = this.leftNodes.find(n => n.id === 'a');
        const rightA = this.rightNodes.find(n => n.id === 'a');

        leftA.status = 'executing';
        rightA.status = 'executing';

        this.leftTimeElapsed = 1;
        this.rightTimeElapsed = 1;

        this.updateTimeDisplays();
    }

    async phase2BaselineHop0() {
        // Left: Hop 0 error, forward to orch
        const leftA = this.leftNodes.find(n => n.id === 'a');
        const leftOrch = this.leftNodes.find(n => n.id === 'orch');

        leftA.status = 'locked';
        leftA.locked = true;
        leftA.statusBadge = 'PEND';
        leftA.statusColor = '#fb8500';

        this.leftMessages = [{
            from: leftA,
            to: leftOrch,
            label: 'PENDING',
            color: '#fb8500'
        }];

        this.leftTimeElapsed += 2;
        this.updateTimeDisplays();
    }

    async phase3TwoPCSimulation() {
        // Right: Forward and simulate
        const rightA = this.rightNodes.find(n => n.id === 'a');
        const rightOrch = this.rightNodes.find(n => n.id === 'orch');
        const rightB = this.rightNodes.find(n => n.id === 'b');
        const rightC = this.rightNodes.find(n => n.id === 'c');

        rightOrch.status = 'simulating';

        this.rightMessages = [
            { from: rightOrch, to: rightA, label: 'Fetch', color: '#3b82f6' },
            { from: rightOrch, to: rightB, label: 'Fetch', color: '#3b82f6' },
            { from: rightOrch, to: rightC, label: 'Fetch', color: '#3b82f6' }
        ];

        this.rightTimeElapsed += 3;
        this.updateTimeDisplays();
    }

    async phase4BaselineHop1() {
        // Left: Route to B, execute, error
        const leftOrch = this.leftNodes.find(n => n.id === 'orch');
        const leftB = this.leftNodes.find(n => n.id === 'b');

        leftB.status = 'locked';
        leftB.locked = true;
        leftB.statusBadge = 'PEND';
        leftB.statusColor = '#fb8500';

        this.leftMessages = [{
            from: leftB,
            to: leftOrch,
            label: 'PENDING',
            color: '#fb8500'
        }];

        this.leftTimeElapsed += 3;
        this.updateTimeDisplays();
    }

    async phase5TwoPCPrepare() {
        // Right: Broadcast PREPARE, collect votes
        const rightOrch = this.rightNodes.find(n => n.id === 'orch');
        const rightA = this.rightNodes.find(n => n.id === 'a');
        const rightB = this.rightNodes.find(n => n.id === 'b');
        const rightC = this.rightNodes.find(n => n.id === 'c');

        rightOrch.status = 'idle';
        rightA.status = 'locked';
        rightA.locked = true;
        rightA.statusBadge = 'YES';
        rightA.statusColor = '#06d6a0';

        rightB.status = 'locked';
        rightB.locked = true;
        rightB.statusBadge = 'YES';
        rightB.statusColor = '#06d6a0';

        rightC.status = 'locked';
        rightC.locked = true;
        rightC.statusBadge = 'YES';
        rightC.statusColor = '#06d6a0';

        this.rightMessages = [
            { from: rightA, to: rightOrch, label: 'YES', color: '#06d6a0' },
            { from: rightB, to: rightOrch, label: 'YES', color: '#06d6a0' },
            { from: rightC, to: rightOrch, label: 'YES', color: '#06d6a0' }
        ];

        this.rightTimeElapsed += 2;
        this.updateTimeDisplays();
    }

    async phase6BaselineHop2() {
        // Left: Route to C, success
        const leftOrch = this.leftNodes.find(n => n.id === 'orch');
        const leftC = this.leftNodes.find(n => n.id === 'c');

        leftC.status = 'success';
        leftC.locked = true;

        this.leftMessages = [{
            from: leftC,
            to: leftOrch,
            label: 'SUCCESS',
            color: '#06d6a0'
        }];

        this.leftTimeElapsed += 3;
        this.updateTimeDisplays();
    }

    async phase7TwoPCCommit() {
        // Right: COMMIT, atomic finalize
        const rightOrch = this.rightNodes.find(n => n.id === 'orch');
        const rightA = this.rightNodes.find(n => n.id === 'a');
        const rightB = this.rightNodes.find(n => n.id === 'b');
        const rightC = this.rightNodes.find(n => n.id === 'c');

        rightA.status = 'success';
        rightA.locked = false;
        rightA.statusBadge = null;

        rightB.status = 'success';
        rightB.locked = false;
        rightB.statusBadge = null;

        rightC.status = 'success';
        rightC.locked = false;
        rightC.statusBadge = null;

        this.rightMessages = [];

        this.rightTimeElapsed += 1;
        this.updateTimeDisplays();
    }

    async phase8BaselineFinalize() {
        // Left: Broadcast SUCCESS, finalize all
        const leftA = this.leftNodes.find(n => n.id === 'a');
        const leftB = this.leftNodes.find(n => n.id === 'b');
        const leftC = this.leftNodes.find(n => n.id === 'c');

        leftA.status = 'success';
        leftA.locked = false;
        leftA.statusBadge = null;

        leftB.status = 'success';
        leftB.locked = false;
        leftB.statusBadge = null;

        leftC.status = 'success';
        leftC.locked = false;

        this.leftMessages = [];

        this.leftTimeElapsed += 1;
        this.updateTimeDisplays();
    }

    reset() {
        this.phase = 0;
        this.isPlaying = false;
        this.leftTimeElapsed = 0;
        this.rightTimeElapsed = 0;
        this.leftMessages = [];
        this.rightMessages = [];

        this.leftNodes.forEach(n => {
            n.status = 'idle';
            n.locked = false;
            n.statusBadge = null;
        });

        this.rightNodes.forEach(n => {
            n.status = 'idle';
            n.locked = false;
            n.statusBadge = null;
        });

        this.updateTimeDisplays();
        this.render();
    }

    updateTimeDisplays() {
        this.leftTime.text(`Time: ${this.leftTimeElapsed}s`);
        this.rightTime.text(`Time: ${this.rightTimeElapsed}s`);
    }

    sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }
}

// Initialize comparison visualization
let comparisonViz = null;

document.addEventListener('DOMContentLoaded', () => {
    comparisonViz = new ComparisonVisualization();

    document.getElementById('comparison-play').addEventListener('click', () => {
        comparisonViz.play();
    });

    document.getElementById('comparison-pause').addEventListener('click', () => {
        comparisonViz.pause();
    });

    document.getElementById('comparison-reset').addEventListener('click', () => {
        comparisonViz.reset();
    });

    document.getElementById('comparison-speed').addEventListener('input', (e) => {
        comparisonViz.speed = parseFloat(e.target.value);
    });
});
