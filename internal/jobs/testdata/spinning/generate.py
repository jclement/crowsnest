# Writes the spinning detector's fixtures: transcripts in Claude Code's JSONL shape (assistant
# tool_use lines, user tool_result lines, ISO timestamps), constructed to the patterns below.
import json, sys, datetime, os
out_dir = sys.argv[1]
class T:
    def __init__(s): s.lines=[]; s.t=datetime.datetime(2026,11,27,9,0,0); s.n=0
    def call(s, name, inp, result='', err=False, gap=30):
        s.t += datetime.timedelta(seconds=gap); s.n += 1; i='toolu_%03d'%s.n
        ts=s.t.strftime('%Y-%m-%dT%H:%M:%S.000Z')
        s.lines.append({'type':'assistant','timestamp':ts,'message':{'role':'assistant','content':[{'type':'text','text':'Next step.'},{'type':'tool_use','id':i,'name':name,'input':inp}]}})
        s.lines.append({'type':'user','timestamp':ts,'message':{'role':'user','content':[{'type':'tool_result','tool_use_id':i,'content':result,'is_error':err}]}})
    def save(s, name): open(os.path.join(out_dir,name),'w').write('\n'.join(json.dumps(l) for l in s.lines)+'\n')
FAIL='--- FAIL: TestWidgetParse (0.00s)\n    widget_test.go:14: expected 2, got 3\nFAIL\nexit status 1'
FAIL2='--- FAIL: TestWidgetRender (0.00s)\nFAIL\nexit status 1'
# 1. Spinning: the same test and the same two reads, over and over, nothing edited.
t=T()
for k in range(40):
    t.call('Bash',{'command':'go test ./internal/widget/','description':'run tests'},FAIL,True)
    t.call('Read',{'file_path':'/repo/internal/widget/widget.go'},'package widget ...')
    if k%3==0: t.call('Grep',{'pattern':'func Parse','path':'/repo/internal/widget'},'widget.go:10')
t.save('spin-test-read-loop.jsonl')
# 2. Spinning: re-applying the same failed edit (it errors each time) and re-running the test.
t=T()
for k in range(35):
    t.call('Edit',{'file_path':'/repo/a.go','old_string':'x := 1','new_string':'x := 2'},'String to replace not found in file.',True)
    t.call('Bash',{'command':'go test ./...'},FAIL,True,gap=20)
t.save('spin-failed-edit-loop.jsonl')
# 3. Spinning: polling a CI status every 30 s for 25 minutes (busy waiting, no change).
t=T()
for k in range(50):
    t.call('Bash',{'command':'gh run view 1234567 --json status'},'{"status":"in_progress"}')
t.save('spin-polling.jsonl')
# 4. Healthy: a builder's red-green cycle: edit, test, a new failure, edit, commit.
t=T()
files=['widget.go','parse.go','render.go','widget_test.go','parse_test.go']
for k in range(45):
    f=files[k%len(files)]
    t.call('Read',{'file_path':'/repo/'+f},'...')
    t.call('Edit',{'file_path':'/repo/'+f,'old_string':'a%d'%k,'new_string':'b%d'%k},'ok')
    t.call('Bash',{'command':'go test ./...'},FAIL if k%2 else FAIL2,True)
    if k%9==8: t.call('Bash',{'command':'git commit -am "step %d"'%k},'[job 1] step')
t.save('healthy-red-green.jsonl')
# 5. Healthy: exploring a large codebase: many different greps and reads, no edits yet.
t=T()
for k in range(60):
    t.call('Grep',{'pattern':'symbol%d'%(k*7),'path':'/repo'},'hits')
    t.call('Read',{'file_path':'/repo/pkg%d/file.go'%k},'...')
t.save('healthy-exploring.jsonl')
# 6. Healthy: a long suite run repeatedly while fixing one test at a time (failing set shrinks).
t=T()
names=['TestA','TestB','TestC','TestD','TestE','TestF']
for k in range(36):
    left=names[k//6:]
    res='\n'.join('--- FAIL: %s (0.00s)'%n for n in left)+'\nFAIL' if left else 'ok'
    t.call('Bash',{'command':'go test ./internal/...'},res,bool(left),gap=40)
    t.call('Read',{'file_path':'/repo/x_test.go'},'...')
t.save('healthy-shrinking-failures.jsonl')
# 7. Healthy: short (fewer calls than the window), even if repetitive.
t=T()
for k in range(12): t.call('Bash',{'command':'go test ./...'},FAIL,True)
t.save('healthy-short.jsonl')
# 8. Healthy: repetitive but fast (30 calls in 4 minutes: too soon to tell).
t=T()
for k in range(40): t.call('Bash',{'command':'go vet ./...'},'',False,gap=6)
t.save('healthy-fast-burst.jsonl')
