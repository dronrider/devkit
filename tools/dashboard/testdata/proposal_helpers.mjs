import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source = fs.readFileSync(process.argv[2], 'utf8');
const start = source.indexOf('function proposalDraft(');
assert(start >= 0);
const context = vm.createContext({});
vm.runInContext(source.slice(start) + '\nthis.helpers = {proposalDraft, proposalAnswer, proposalComment};', context);
const {proposalDraft, proposalAnswer, proposalComment} = context.helpers;
const shown = JSON.parse(process.argv[3]);
const draft = proposalDraft(shown, 'свои слова', [{source:'XR-1',proposal:'b',revision:2,action:'accept'}]);
shown.sources[0].basis = 'new'; shown.sources[0].proposals.push({id:'new',revision:1});
const restored = JSON.parse(JSON.stringify(draft));
assert.equal(restored.text,'свои слова');
assert.throws(() => proposalAnswer(restored), /весь разбор/);
restored.decisions = [
  {source:'XR-1',proposal:'a',revision:1,action:'accept'},
  {source:'XR-1',proposal:'b',revision:2,action:'accept'},
];
let result = proposalAnswer(restored);
assert.equal(result.snapshots[0].digest,'old');
assert.equal(result.snapshots[0].revision,shown.sources[0].revision);
assert.equal(result.decisions.length,2);
assert.equal(result.snapshots.length,1);
restored.decisions.push({source:'XR-2',proposal:'c',revision:1,action:'accept'});
result = proposalAnswer(restored);
assert.equal(result.decisions.length,3);
assert.equal(result.snapshots.length,2);
assert.equal(proposalComment(restored).decisions.length,0);
assert.equal(proposalComment(restored).mode,'comment');
restored.decisions = [{source:'XR-1',proposal:'a',revision:1,action:'reject',reason:'лишнее'}];
assert.throws(() => proposalAnswer(restored), /не показана/);
restored.decisions = [{source:'XR-1',proposal:'new',revision:1,action:'accept'}];
assert.throws(() => proposalAnswer(restored), /не показана/);
restored.decisions = [{source:'XR-2',proposal:'c',revision:1,action:'accept'}];
delete restored.shown.sources[1].revision;
assert.throws(() => proposalAnswer(restored), /актуальный разбор/);
const statusStart = source.indexOf('function draftCardStatus(');
const statusEnd = source.indexOf('\n}', statusStart);
const statusCtx = vm.createContext({});
vm.runInContext(source.slice(statusStart, statusEnd + 2) + '\nthis.card = draftCardStatus;', statusCtx);
for (const [outcome, status, want] of [
  ['add', '', 'На согласовании'], ['add', 'accepted', 'Подтверждена'], ['add', 'rejected', 'Отклонена'],
  ['attach', '', 'На согласовании'], ['attach', 'accepted', 'Подтверждена'],
  ['defer', '', 'На согласовании'], ['defer', 'rejected', 'Отклонена'],
  ['drop', '', 'На согласовании'], ['drop', 'accepted', 'Подтверждено'], ['drop', 'rejected', 'Отклонено'],
]) assert.equal(statusCtx.card(outcome, status), want);
const lineStart = source.indexOf('function preparedLineText(');
const lineEnd = source.indexOf('\n}', lineStart);
const lineCtx = vm.createContext({});
vm.runInContext(source.slice(lineStart, lineEnd + 2) + '\nthis.line = preparedLineText;', lineCtx);
assert.equal(lineCtx.line({source:'XR-1',task:{id:'b'},title:'Новая запись'}, 'demo'), 'Новая запись: #demo/draft/XR-1/prepared/b');
console.log('proposal whole bundle helpers: ok');
