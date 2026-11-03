import assert from 'node:assert/strict';
import { makeSandbox, makeNode, allByClass } from './poc_dom.mjs';
const [app, raw] = process.argv.slice(2);
const {sandbox,store} = makeSandbox(app, () => ({}));
const shown=JSON.parse(raw);
const feed=makeNode('div'), ta=makeNode('textarea'); ta.value='свои слова';
const pick={addr:'synthetic',on:true,ask:{kind:'agent',task:'XR-1',since:1,text:'Подготовил задачи, согласуй их описание.',prepared:shown}};
sandbox.preparedPickWire(feed,pick,shown);
// Реплики вопроса в ленте нет: строки дописываются пузырём той же формы, галки
// вешаются на них (DK-1469, решение 4).
assert.equal(allByClass(feed,'askfb').length,1);
const marks=allByClass(feed,'caskpick');
assert.equal(marks.length,3);
// Текст узла собирается с детей: сам узел стенда держит только разметку.
const lines=[...feed.querySelectorAll('p')].map((p)=>sandbox.askPickText(p).replace(/\s+/g,' ').trim());
assert.equal(lines.length,5);
assert.equal(lines[0].startsWith('вопрос по XR-1'),true);
assert.equal(lines[1].startsWith('Подготовил задачи'),true);
assert.equal(lines[2].startsWith('a:'),true);
assert.ok(lines[2].includes('#worktree/draft/XR-1/prepared/a'));
const comment = sandbox.proposalComment(pick.proposalDraft);
assert.equal(comment.mode, 'comment');
assert.equal(comment.snapshots.length, 2);
assert.equal(comment.decisions.length, 0);
// Отмеченная галкой задача подтверждается, две отметки несут один снимок своей
// записи, и отметка переживает перерисовку памятью разговора.
const click={stopPropagation(){}};
marks[0].handlers.click(click);
marks[1].handlers.click(click);
const saved=JSON.parse(store.get('devkit.askdraft.synthetic'));
assert.equal(saved.proposalDraft.decisions.length,2);
assert.equal(saved.proposalDraft.decisions[0].action,'accept');
// Повторная развеска того же вопроса читает отметки из памяти: галки стоят
// на местах, чужого решения в них не появилось.
const feed2=makeNode('div');
sandbox.preparedPickWire(feed2,pick,shown);
const again=allByClass(feed2,'caskpick');
assert.equal(again.length,3);
assert.equal(again[0].classList.contains('on'),true);
assert.equal(again[1].classList.contains('on'),true);
assert.equal(again[2].classList.contains('on'),false);
// Ответ собирается по показанному снимку, а не по свежему материалу записи.
shown.sources[0].basis='новый';
const answer=sandbox.proposalAnswer(saved.proposalDraft);
assert.equal(answer.snapshots[0].digest,'old');
assert.equal(answer.decisions.length,2);
console.log('proposal UI marks and persistence: ok');
