const test=require('node:test');
const assert=require('node:assert/strict');
const {route,intersects,labelPosition,coverageMetric,complexityMetric,bounds,gaugeOptions,wheelViewport}=require('./graph-tools.js');
const rects=[
  {id:'a',x:0,y:0,w:100,h:60},
  {id:'block',x:0,y:110,w:100,h:60},
  {id:'b',x:0,y:220,w:100,h:60}
];
test('ordinary trackpad scrolling moves both viewport axes without changing zoom',()=>{
  const pan={x:100,y:200,w:800,h:600};
  const next=wheelViewport(pan,{deltaX:20,deltaY:-40,deltaMode:0},
    {a:2,b:0,c:0,d:2},500);
  assert.deepEqual(next,{x:140,y:120,w:800,h:600});
  assert.deepEqual(pan,{x:100,y:200,w:800,h:600});
});
test('line and page wheel deltas scroll at the rendered SVG scale',()=>{
  const pan={x:0,y:0,w:800,h:600},inverse={a:.5,b:0,c:0,d:.5};
  assert.deepEqual(wheelViewport(pan,{deltaX:1,deltaY:2,deltaMode:1},inverse,400),
    {x:8,y:16,w:800,h:600});
  assert.deepEqual(wheelViewport(pan,{deltaX:0,deltaY:1,deltaMode:2},inverse,400),
    {x:0,y:200,w:800,h:600});
});
test('Shift scroll and browser pinch wheel events zoom instead of scrolling',()=>{
  const pan={x:100,y:200,w:800,h:600},inverse={a:1,b:0,c:0,d:1};
  for(const modifier of ['shiftKey','ctrlKey']) {
    const out=wheelViewport(pan,{deltaX:0,deltaY:100,deltaMode:0,[modifier]:true},inverse,400);
    const into=wheelViewport(pan,{deltaX:0,deltaY:-100,deltaMode:0,[modifier]:true},inverse,400);
    assert.ok(out.w>800&&out.h>600);
    assert.ok(into.w<800&&into.h<600);
    assert.equal(out.x+out.w/2,500);
    assert.equal(out.y+out.h/2,500);
    assert.ok(Math.abs(out.w/out.h-4/3)<1e-12);
  }
});
test('Shift horizontal-wheel remapping still zooms and zero deltas do not zoom',()=>{
  const pan={x:100,y:200,w:800,h:600},inverse={a:1,b:0,c:0,d:1};
  const next=wheelViewport(pan,{deltaX:50,deltaY:0,shiftKey:true,deltaMode:0},inverse,400);
  assert.ok(next.w>pan.w);
  assert.deepEqual(wheelViewport(pan,{deltaX:0,deltaY:0,ctrlKey:true,deltaMode:0},inverse,400),pan);
});
function clearPath(points,rectangles) {
  for(let i=1;i<points.length;i++){
    assert.ok(points[i-1].x===points[i].x||points[i-1].y===points[i].y,'orthogonal segment');
    for(const rect of rectangles)assert.equal(intersects(points[i-1],points[i],rect),false,`edge crosses ${rect.id}`);
  }
}
test('long edge avoids intervening nodes and attaches outside source/target interiors',()=>{
  const points=route(rects,'a','b',0);
  assert.ok(points.length>=4);
  clearPath(points,rects);
});
test('back edges, self loops and moved nodes retain visible routes',()=>{
  for(const [from,to] of [['b','a'],['a','a']])clearPath(route(rects,from,to,1),rects);
  const moved=rects.map(r=>r.id==='block'?{...r,x:70}:r);
  clearPath(route(moved,'a','b',2),moved);
});
test('routing ports fit inside forty-pixel lanes even on the fourth parallel edge',()=>{
  const grid=[{id:'a',x:0,y:0,w:230,h:104},{id:'middle',x:270,y:0,w:230,h:104},{id:'b',x:540,y:0,w:230,h:104}];
  const points=route(grid,'a','b',3);
  assert.equal(points.obstructed,false);
  clearPath(points,grid);
});
test('direct adjacent nodes do not require a detour and bounds include routed edges',()=>{
  const pair=[rects[0],{...rects[2],x:220,y:0}];
  const points=route(pair,'a','b',0);
  clearPath(points,pair);
  const box=bounds(pair,[{x:-90,y:-30}]);
  assert.ok(box.x<=-90&&box.y<=-30&&box.x+box.w>=320&&box.y+box.h>=60);
});
test('reciprocal architectural contracts use separate ports without crossing nodes',()=>{
  const pair=[{id:'a',x:0,y:0,w:320,h:142},{id:'b',x:0,y:250,w:320,h:142}];
  const input=route(pair,'a','b',0,-16),output=route(pair,'b','a',1,16);
  clearPath(input,pair);clearPath(output,pair);
  assert.notEqual(input[0].x,output.at(-1).x);
  assert.notEqual(input.at(-1).x,output[0].x);
});
test('architectural contract labels avoid node interiors and previously placed labels',()=>{
  const rectangles=[{id:'a',x:460,y:0,w:320,h:142},
    {id:'b',x:0,y:250,w:320,h:142},{id:'c',x:460,y:250,w:320,h:142}];
  const points=route(rectangles,'a','b',0,-16),occupied=[];
  for(let i=0;i<2;i++) {
    const label=labelPosition(points,230,rectangles,occupied,-16);
    assert.ok(label,'a visible placement is available');
    for(const box of [...rectangles,...occupied])
      assert.equal(label.x<box.x+box.w&&label.x+label.w>box.x&&label.y<box.y+box.h&&label.y+label.h>box.y,false);
    occupied.push(label);
  }
});
test('coverage gauge uses statement weights, not percentage of blocks',()=>{
  const loc={fileID:'f',line:1,column:1,endLine:10,endColumn:1};
  const coverage={blocks:[
    {fileID:'f',startLine:2,startColumn:1,endLine:3,endColumn:1,statements:9,count:1},
    {fileID:'f',startLine:4,startColumn:1,endLine:5,endColumn:1,statements:1,count:0},
    {fileID:'g',startLine:4,startColumn:1,endLine:5,endColumn:1,statements:80,count:0}
  ]};
  assert.equal(coverageMetric(loc,coverage).value,90);
  assert.equal(coverageMetric(loc,coverage).tone,'green');
  assert.equal(coverageMetric({...loc,fileID:'missing'},coverage).value,null);
  assert.equal(coverageMetric(loc,null).tone,'unknown');
  assert.equal(coverageMetric(loc,{blocks:[{...coverage.blocks[0],statements:0}]}).value,null);
});
test('complexity keeps actual count and fixed disclosed inverted color thresholds',()=>{
  for(const [value,tone] of [[1,'green'],[5,'green'],[6,'amber'],[10,'amber'],[11,'orange'],[20,'orange'],[21,'red'],[120,'red']]){
    const metric=complexityMetric(value);
    assert.equal(metric.value,value);assert.equal(metric.tone,tone);
  }
  assert.equal(complexityMetric(undefined).value,null);
});
test('package coverage combines file ranges once without double-counting overlaps',()=>{
  const locations=['a','b','a'].map(fileID=>({fileID,line:1,column:1,endLine:20,endColumn:1}));
  const blocks=['a','b'].map((fileID,i)=>({fileID,startLine:1,startColumn:1,endLine:2,endColumn:1,statements:i?3:1,count:i?0:1}));
  assert.equal(coverageMetric(locations,{blocks}).value,25);
  assert.equal(coverageMetric(locations,{blocks}).total,4);
});
test('standard gauge uses the full status-colored face even at maximum complexity',()=>{
  for(const [kind,value,color] of [
    ['complexity',25,'#ef4444'],['complexity',120,'#ef4444'],
    ['complexity',5,'#159447'],['complexity',6,'#429f43'],
    ['complexity',10,'#f4cb32'],['complexity',20,'#f59b23'],
    ['coverage',0,'#ef4444'],['coverage',40,'#f59b23'],
    ['coverage',60,'#f4cb32'],['coverage',80,'#159447'],['coverage',100,'#159447']
  ]) {
    const options=gaugeOptions(kind,value);
    assert.equal(options.gaugeColor,color);
    assert.deepEqual(options.levelColors,[color]);
    assert.equal(options.gaugeWidthScale,8/3);
    assert.equal(options.pointer,true);
    assert.equal(options.value,value);
  }
});
test('gauge keeps raw metric scale, high-contrast needle and disables animation',()=>{
  assert.equal(gaugeOptions('coverage',50).max,100);
  const options=gaugeOptions('complexity',120);
  assert.equal(options.max,25);
  assert.equal(options.value,120);
  assert.equal(options.pointerOptions.color,'#ffffff');
  assert.equal(options.pointerOptions.stroke,'#111827');
  assert.ok(options.pointerOptions.stroke_width>=1.5);
  assert.equal(options.startAnimationTime,0);
  assert.equal(options.refreshAnimationTime,0);
});
test('missing evidence stays neutral without a misleading zero needle',()=>{
  for(const kind of ['coverage','complexity']) {
    const options=gaugeOptions(kind,null);
    assert.equal(options.pointer,false);
    assert.equal(options.gaugeColor,'#465367');
    assert.deepEqual(options.levelColors,['#465367']);
    assert.equal(options.hideValue,true);
  }
});
