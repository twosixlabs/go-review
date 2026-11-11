(function(root) {
  'use strict';
  function walk(edges, id, reverse, depth) {
    const adjacent=new Map(), distances=new Map([[id,0]]), queue=[id];
    for(const edge of edges) {
      const from=reverse?edge.to:edge.from, to=reverse?edge.from:edge.to;
      if(!adjacent.has(from))adjacent.set(from,[]);
      adjacent.get(from).push(to);
    }
    for(let i=0;i<queue.length;i++) {
      const current=queue[i], distance=distances.get(current);
      if(distance>=depth)continue;
      for(const next of adjacent.get(current)||[])if(!distances.has(next)) {
        distances.set(next,distance+1);queue.push(next);
      }
    }
    return distances;
  }
  function slice(graph, id, direction, depth) {
    if(!graph.nodes.some(n=>n.id===id))return {nodes:[],edges:[]};
    const ids=new Set([id]);
    if(direction!=='downstream')for(const key of walk(graph.edges,id,true,depth).keys())ids.add(key);
    if(direction!=='upstream')for(const key of walk(graph.edges,id,false,depth).keys())ids.add(key);
    return {nodes:graph.nodes.filter(n=>ids.has(n.id)),edges:graph.edges.filter(e=>ids.has(e.from)&&ids.has(e.to))};
  }
  function positions(graph, id) {
    const levels=new Map([[id,0]]);
    for(const [key,distance] of walk(graph.edges,id,true,Infinity))levels.set(key,-distance);
    for(const [key,distance] of walk(graph.edges,id,false,Infinity))levels.set(key,distance);
    const minimum=Math.min(0,...levels.values()), last=Math.max(0,...levels.values())+1, slots=new Map();
    return new Map(graph.nodes.map(node=>{
      const rank=(levels.has(node.id)?levels.get(node.id):last)-minimum, slot=slots.get(rank)||0;
      slots.set(rank,slot+1);
      return [node.id,{rank,slot}];
    }));
  }
  const api={slice,positions};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;
  else root.GoReviewDataFlow=api;
})(typeof globalThis==='undefined'?this:globalThis);
