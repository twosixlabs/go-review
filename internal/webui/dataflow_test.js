const test = require('node:test');
const assert = require('node:assert/strict');
const {slice, positions} = require('./dataflow-tools.js');

const graph = {
  nodes:['parameter','constant','merge','add','return','other'].map(id=>({id})),
  edges:[
    {from:'parameter',to:'merge'}, {from:'constant',to:'merge'},
    {from:'merge',to:'add'}, {from:'add',to:'merge'}, {from:'add',to:'return'}
  ]
};

test('data-flow neighborhood includes exact direct definitions and consumers', () => {
  const result=slice(graph,'merge','both',1);
  assert.deepEqual(result.nodes.map(n=>n.id),['parameter','constant','merge','add']);
  assert.equal(result.edges.length,4);
});

test('upstream and downstream traces terminate on cyclic phi edges', () => {
  assert.deepEqual(slice(graph,'merge','upstream',Infinity).nodes.map(n=>n.id),['parameter','constant','merge','add']);
  assert.deepEqual(slice(graph,'merge','downstream',Infinity).nodes.map(n=>n.id),['merge','add','return']);
  assert.equal(slice(graph,'missing','both',1).nodes.length,0);
});

test('data-flow layout puts definitions before selected value and consumers after it', () => {
  const p=positions(graph,'merge');
  assert.ok(p.get('parameter').rank<p.get('merge').rank);
  assert.ok(p.get('return').rank>p.get('merge').rank);
  assert.equal(p.size,graph.nodes.length);
  assert.equal(new Set([...p.values()].map(v=>`${v.rank}:${v.slot}`)).size,graph.nodes.length);
});

test('one-hop focus does not invent a path between sibling operands', () => {
  const result=slice(graph,'parameter','both',1);
  assert.deepEqual(result.nodes.map(n=>n.id),['parameter','merge']);
  assert.deepEqual(result.edges,[{from:'parameter',to:'merge'}]);
});

test('zero-depth focus and whole-function expansion preserve original evidence', () => {
  const before=JSON.stringify(graph);
  assert.deepEqual(slice(graph,'return','both',0).nodes.map(n=>n.id),['return']);
  slice(graph,'merge','both',Infinity);
  positions(graph,'merge');
  assert.equal(JSON.stringify(graph),before);
});
