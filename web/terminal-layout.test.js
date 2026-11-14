import test from 'node:test';
import assert from 'node:assert/strict';
import { fittedDimensions, hostHeight, widgetHeight } from './terminal-layout.js';
const pane = { owned:true,connected:true,visible:true,width:800,height:400,proposal:{cols:92,rows:22} };
test('hidden, collapsed and detached views never resize the shared terminal', () => {
  for (const change of [{visible:false},{width:0},{height:0},{connected:false},{proposal:undefined}]) {
    assert.equal(fittedDimensions({...pane,...change}),undefined);
  }
});
test('valid visible dimensions fit within supported bounds', () => {
  assert.deepEqual(fittedDimensions(pane),{cols:92,rows:22});
  assert.deepEqual(fittedDimensions({...pane,proposal:{cols:900,rows:300}}),{cols:500,rows:200});
  assert.equal(fittedDimensions({...pane,proposal:{cols:NaN,rows:20}}),undefined);
});

test('visible observer fits without claiming keyboard ownership', () => {
  assert.deepEqual(fittedDimensions({...pane,owned:false}),{cols:92,rows:22});
});
test('host height accepts fixed and bounded panes without inventing a minimum', () => {
  assert.equal(hostHeight({containerDimensions:{height:340,width:800}}),340);
  assert.equal(hostHeight({containerDimensions:{maxHeight:480}}),480);
  for(const value of [undefined,0,-10,NaN,Infinity])assert.equal(hostHeight({containerDimensions:{height:value}}),undefined);
});

test('catalog height stays bounded even when the host reports a huge available area', () => {
  assert.equal(widgetHeight({containerDimensions:{maxHeight:20000}},true),520);
  assert.equal(widgetHeight({containerDimensions:{height:360}},true),360);
  assert.equal(widgetHeight(undefined,true),520);
  assert.equal(widgetHeight({containerDimensions:{height:800}},false),800);
});

test('workspaces stay compact in chat and follow explicit expanded canvas sizing',()=>{
  assert.equal(widgetHeight({displayMode:'inline',containerDimensions:{maxHeight:20000}},false),520);
  assert.equal(widgetHeight({containerDimensions:{maxHeight:20000}},false),520);
  assert.equal(widgetHeight({containerDimensions:{height:360}},false),360);
  assert.equal(widgetHeight({displayMode:'fullscreen',containerDimensions:{height:980}},false),980);
  assert.equal(widgetHeight({displayMode:'fullscreen',containerDimensions:{maxHeight:20000}},false),720);
  assert.equal(widgetHeight({displayMode:'inline',containerDimensions:{height:980}},false),980);
});

test('expanded maximum-only canvas uses its actual viewport rather than leaving a 720px panel',()=>{
  assert.equal(widgetHeight({displayMode:'fullscreen',containerDimensions:{maxHeight:20000}},false,1100),1100);
  assert.equal(widgetHeight({displayMode:'fullscreen',containerDimensions:{maxHeight:800}},false,1100),800);
  assert.equal(widgetHeight({displayMode:'inline',containerDimensions:{maxHeight:20000}},false,1100),520);
});

test('fixed host panes fill the assigned space even without display mode or in the catalog',()=>{
  for(const displayMode of [undefined,'inline','pip','fullscreen']) {
    for(const catalog of [false,true])assert.equal(widgetHeight({displayMode,containerDimensions:{height:1050}},catalog,1050),1050);
  }
});
test('expanded catalogs and picture-in-picture follow the viewport while inline catalogs stay compact',()=>{
  for(const displayMode of ['pip','fullscreen']) {
    for(const catalog of [false,true]) {
      assert.equal(widgetHeight({displayMode,containerDimensions:{maxHeight:20000}},catalog,1120),1120);
      assert.equal(widgetHeight({displayMode,containerDimensions:{maxHeight:900}},catalog,1120),900);
      assert.equal(widgetHeight({displayMode},catalog,740),740);
    }
  }
  assert.equal(widgetHeight({displayMode:'inline',containerDimensions:{maxHeight:20000}},true,1120),520);
});
