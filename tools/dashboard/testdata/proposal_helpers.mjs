import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source = fs.readFileSync(process.argv[2], 'utf8');
const start = source.indexOf('function proposalDraft(');
assert(start >= 0);
const context = vm.createContext({});
vm.runInContext(source.slice(start) + '\nthis.helpers = {proposalDraft, proposalAnswer};', context);
const {proposalDraft, proposalAnswer} = context.helpers;
const shown = JSON.parse(process.argv[3]);
const draft = proposalDraft(shown, 'свои слова', [{source:'XR-1',proposal:'b',revision:2,action:'accept'}]);
shown.sources[0].basis = 'new'; shown.sources[0].proposals.push({id:'new',revision:1});
const restored = JSON.parse(JSON.stringify(draft));
assert.equal(restored.text,'свои слова');
let result = proposalAnswer(restored);
assert.equal(result.snapshots[0].digest,'old');
assert.equal(result.decisions.length,1);
assert.equal(result.decisions[0].proposal,'b');
// Отмеченные галками задачи подтверждаются: решение про одну задачу несёт
// снимок только её записи.
restored.decisions = [
  {source:'XR-1',proposal:'a',revision:1,action:'accept'},
  {source:'XR-2',proposal:'c',revision:1,action:'accept'},
];
result = proposalAnswer(restored);
assert.equal(result.decisions.length,2);
assert.equal(result.snapshots.length,2);
restored.decisions = [{source:'XR-1',proposal:'a',revision:1,action:'reject'}];
assert.throws(() => proposalAnswer(restored), /причину/);
restored.decisions = [{source:'XR-1',proposal:'a',revision:1,action:'reject',reason:'лишнее'}];
assert.equal(proposalAnswer(restored).decisions.length,1);
restored.decisions.push({source:'XR-1',proposal:'new',revision:1,action:'accept'});
assert.throws(() => proposalAnswer(restored), /не показана/);
const statusStart = source.indexOf('function draftCardStatus(');
assert(statusStart >= 0);
const statusEnd = source.indexOf('\n}', statusStart);
assert(statusEnd > statusStart);
const statusCtx = vm.createContext({});
vm.runInContext(source.slice(statusStart, statusEnd + 2) + '\nthis.card = draftCardStatus;', statusCtx);
for (const [outcome, status, want] of [
  ['add', '', 'На согласовании'], ['add', 'accepted', 'Подтверждена'], ['add', 'rejected', 'Отклонена'],
  ['attach', '', 'На согласовании'], ['attach', 'accepted', 'Подтверждена'],
  ['defer', '', 'На согласовании'], ['defer', 'rejected', 'Отклонена'],
  ['drop', '', 'На согласовании'], ['drop', 'accepted', 'Подтверждено'], ['drop', 'rejected', 'Отклонено'],
  ['', '', 'На согласовании'], ['', 'accepted', 'Подтверждено'], ['', 'rejected', 'Отклонено'],
]) {
  assert.equal(statusCtx.card(outcome, status), want, outcome + ' ' + status);
}
// Строка-ссылка задачи ведёт на экран подготовленной задачи (DK-1469, решение 4):
// заголовок задачи без названия это её ключ.
const lineStart = source.indexOf('function preparedLineText(');
assert(lineStart >= 0);
const lineEnd = source.indexOf('\n}', lineStart);
assert(lineEnd > lineStart);
const lineCtx = vm.createContext({});
vm.runInContext(source.slice(lineStart, lineEnd + 2) + '\nthis.line = preparedLineText;', lineCtx);
assert.equal(lineCtx.line({source:'XR-1',task:{id:'b'},title:'Новая запись'}, 'demo'), 'Новая запись: #demo/draft/XR-1/prepared/b');
assert.equal(lineCtx.line({source:'XR-1',task:{id:'x'},title:'x'} , 'demo'), 'x: #demo/draft/XR-1/prepared/x');
console.log('proposal helpers: ok');
