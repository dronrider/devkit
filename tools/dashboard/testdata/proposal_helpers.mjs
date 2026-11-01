import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source = fs.readFileSync(process.argv[2], 'utf8');
const start = source.indexOf('function proposalDraft(');
assert(start >= 0);
const context = vm.createContext({});
vm.runInContext(source.slice(start) + '\nthis.helpers = {proposalDraft, proposalAnswer, proposalSelect};', context);
const {proposalDraft, proposalAnswer, proposalSelect} = context.helpers;
const shown = JSON.parse(process.argv[3]);
const draft = proposalDraft(shown, 'свои слова', [{source:'XR-1',proposal:'b',revision:2,action:'accept'}]);
shown.sources[0].basis = 'new'; shown.sources[0].proposals.push({id:'new',revision:1});
const restored = JSON.parse(JSON.stringify(draft));
assert.equal(restored.text,'свои слова');
let result = proposalAnswer(restored);
assert.equal(result.snapshots[0].digest,'old');
assert.equal(result.decisions.length,1);
assert.equal(result.decisions[0].proposal,'b');
restored.decisions = proposalSelect(restored.shown, null, 'accept');
result = proposalAnswer(restored);
assert.equal(result.decisions.length,3);
assert.equal(result.snapshots.length,2);
restored.decisions = proposalSelect(restored.shown, 'XR-1', 'reject');
assert.throws(() => proposalAnswer(restored), /причину/);
restored.decisions = proposalSelect(restored.shown, 'XR-1', 'reject', 'лишнее');
assert.equal(proposalAnswer(restored).decisions.length,2);
restored.decisions.push({source:'XR-1',proposal:'new',revision:1,action:'accept'});
assert.throws(() => proposalAnswer(restored), /не показана/);
const statusStart = source.indexOf('function draftCardStatus(');
assert(statusStart >= 0);
const statusEnd = source.indexOf('\n}', statusStart);
assert(statusEnd > statusStart);
const statusCtx = vm.createContext({});
vm.runInContext(source.slice(statusStart, statusEnd + 2) + '\nthis.card = draftCardStatus;', statusCtx);
for (const [outcome, status, want] of [
  ['add', '', 'Подготовлена'], ['add', 'accepted', 'Подтверждена'], ['add', 'rejected', 'Отклонена'],
  ['attach', '', 'Подготовлена'], ['attach', 'accepted', 'Подтверждена'],
  ['defer', '', 'Подготовлена'], ['defer', 'rejected', 'Отклонена'],
  ['drop', '', 'Подготовлено'], ['drop', 'accepted', 'Подтверждено'], ['drop', 'rejected', 'Отклонено'],
  ['', '', 'Подготовлено'], ['', 'accepted', 'Подтверждено'], ['', 'rejected', 'Отклонено'],
]) {
  assert.equal(statusCtx.card(outcome, status), want, outcome + ' ' + status);
}
const introStart = source.indexOf('function draftIntroText(');
assert(introStart >= 0);
const introEnd = source.indexOf('\n}', introStart);
assert(introEnd > introStart);
const introCtx = vm.createContext({});
vm.runInContext(source.slice(introStart, introEnd + 2) + '\nthis.intro = draftIntroText;', introCtx);
assert.match(introCtx.intro([{proposals:[{outcome:'add'},{outcome:'drop'}]}]), /^Задача подготовлена\./);
assert.match(introCtx.intro([{proposals:[{outcome:'drop'}]}]), /^Карточки подготовлены\./);
assert.match(introCtx.intro([]), /^Карточки подготовлены\./);
console.log('proposal helpers: ok');
