import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
let Processor;
const sandbox = {
  AudioWorkletProcessor: class { constructor() { this.port = { postMessage() {} }; } },
  registerProcessor(_name, ctor) { Processor = ctor; },
  Float32Array,
};
vm.runInNewContext(readFileSync(new URL("../internal/relay/web/playback-processor.js", import.meta.url), "utf8"), sandbox);
const make = () => new Processor({ processorOptions: { ringSamples: 3200, prefillSamples: 320 } });
const audio = (p, position, size=320, value=0.5) => p.receive({type:"audio",position,samples:new Float32Array(size).fill(value)});
const pull = p => { const out=new Float32Array(128);p.process([],[[out]]);return out; };
const p=make();
p.receive({type:"quiet",position:0});
for(let i=0;i<10000;i++) assert.ok(pull(p).every(x=>x===0));
assert.equal(p.underruns,0);
audio(p,9600000);
assert.ok(pull(p).every(x=>x===0.5));
p.receive({type:"quiet",position:9600320});
pull(p);pull(p);
assert.equal(p.underruns,0,"intentional end must drain without an underrun");
assert.ok(pull(p).every(x=>x===0));
audio(p,9700000);
assert.ok(pull(p).every(x=>x===0.5));
p.receive({type:"reset"});
assert.ok(pull(p).every(x=>x===0),"reset must discard previous session");
audio(p,0,3200);
audio(p,3200,320);
assert.equal(p.dropped,320,"overflow drops oldest samples");
const gap=make();audio(gap,0);pull(gap);pull(gap);pull(gap);
assert.equal(gap.underruns,1,"unexpected starvation must count");
const short=make();audio(short,0,128);short.receive({type:"quiet",position:128});
assert.ok(pull(short).every(x=>x===0.5),"quiet transition must drain below-prefill tail");
console.log("Playback: quiet joins, long silence, onset, tails, reset, drops and underruns pass.");
const adjacent=make();
audio(adjacent,0,128);
adjacent.receive({type:"quiet",position:128});
audio(adjacent,256,320,0.75);
assert.ok(pull(adjacent).every(x=>x===0.5),"resume cannot erase preceding tail");
assert.ok(pull(adjacent).every(x=>x===0),"short quiet interval must retain timing");
assert.ok(pull(adjacent).every(x=>x===0.75),"new onset must follow silence");
// Parse the inline module too; node --check of codec/worklet alone misses it.
const { spawnSync } = await import("node:child_process");
const html=readFileSync(new URL("../internal/relay/web/index.html",import.meta.url),"utf8");
const inline=html.match(/<script type="module">([\s\S]*?)<\/script>/)[1];
const check=spawnSync(process.execPath,["--input-type=module","--check"],{input:inline,encoding:"utf8"});
assert.equal(check.status,0,check.stderr);
