"""Opt-in live READ-ONLY acceptance against dedicated, already-existing test objects.

Never creates a topic/group, produces records, commits offsets or changes config.
The supplied group must be dedicated and idle while comparing offsets around peek.
No raw credentials, endpoints, member hosts or message samples are printed.
"""
import argparse
import json
import os
from pathlib import Path
import queue
import subprocess
import threading
import time
import uuid


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary', type=Path, required=True)
    p.add_argument('--targets', type=Path, required=True)
    p.add_argument('--target', required=True)
    p.add_argument('--topic', required=True)
    p.add_argument('--group', required=True)
    p.add_argument('--config-topic', required=True)
    p.add_argument('--expected-retention-ms', required=True)
    a = p.parse_args()
    proc = subprocess.Popen([str(a.binary.resolve())], stdin=subprocess.PIPE,
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                            text=True, encoding='utf-8', env=dict(os.environ,
                            KAFKA_TARGETS_FILE=str(a.targets.resolve()), KAFKA_MCP_READ_ONLY='true'))
    output = queue.Queue()
    def reader():
        for line in proc.stdout:
            output.put(line)
        output.put(None)
    threading.Thread(target=reader, daemon=True).start()
    sequence = 0
    def send(method, params, notify=False):
        nonlocal sequence
        sequence += 1
        req = dict(jsonrpc='2.0', method=method, params=params)
        if not notify:
            req['id'] = sequence
        proc.stdin.write(json.dumps(req)+'\n')
        proc.stdin.flush()
        if notify:
            return
        deadline = time.monotonic()+25
        while True:
            remaining = deadline-time.monotonic()
            if remaining <= 0:
                raise TimeoutError('MCP response timeout')
            line = output.get(timeout=remaining)
            if line is None:
                raise RuntimeError('server stopped before response')
            r = json.loads(line)
            if 'id' not in r and r.get('method', '').startswith('notifications/'):
                continue
            break
        if r.get('id') != sequence or 'error' in r:
            raise RuntimeError('unexpected JSON-RPC response for '+method+': '+json.dumps({
                'id':r.get('id'),'method':r.get('method'),'error':r.get('error')},ensure_ascii=True))
        return r['result']
    def envelope(name, **args):
        result = send('tools/call', dict(name=name, arguments=dict(target=a.target, **args)))
        env = json.loads(next(c['text'] for c in result['content'] if c['type']=='text'))
        return result, env
    def call(name, allow_truncated=False, **args):
        result, env = envelope(name, **args)
        if result.get('isError') or env['status'] != 'ok' or (env['truncated'] and not allow_truncated):
            raise RuntimeError(name+' did not return a complete successful result: '+env['status'])
        return env['data']
    def commits(data):
        return sorted((o['topic'],o['partition'],o['committed_offset']) for o in data['committed_offsets'])
    try:
        init = send('initialize', dict(protocolVersion='2024-11-05',capabilities={},clientInfo=dict(name='readonly-live-acceptance',version='1')))
        send('notifications/initialized', {}, True)
        listed = send('tools/list', {})['tools']
        assert len(listed)==10 and all(t['annotations']['readOnlyHint'] for t in listed)
        call('kafka_capabilities')
        topic_list = call('kafka_topics_list', query=a.topic, limit=200)
        group_list = call('kafka_groups_list', query=a.group, limit=200)
        assert any(t['topic']==a.topic for t in topic_list['topics']), 'fixture topic not listed'
        assert any(g['group']==a.group for g in group_list['groups']), 'fixture group not listed'
        first_page = call('kafka_topics_list', limit=1)
        if first_page['has_more']:
            next_page = call('kafka_topics_list', limit=1, after=first_page['next_after'])
            assert next_page['topics'] and next_page['topics'][0]['topic']>first_page['next_after']
        configs = []
        for params in ({}, {'config_keys': []}):
            data = call('kafka_configs_query', resource_type='topic',resource_names=[a.config_topic], **params)
            configs.append({v['name']:v['value'] for v in data['resources'][0]['configs']})
        assert configs[0] == configs[1] and configs[0]['retention.ms']==a.expected_retention_ms
        topology = call('kafka_topic_inspect', topic=a.topic)['topics'][0]
        assert topology['status']=='ok' and topology['partitions']
        before = call('kafka_group_inspect', group=a.group, topics=[a.topic])
        assert before['state']=='Empty' and not before['members'], 'test group must be idle'
        assert len(before['committed_offsets'])==len(topology['partitions'])
        assert all(o['status']=='ok' for o in before['committed_offsets'])
        partition = min(p['partition'] for p in topology['partitions'])
        bounds = call('kafka_offsets_query', topic=a.topic, partition=partition)
        assert bounds['earliest']['status']=='ok' and bounds['last_stable_offset']['status']=='ok'
        assert bounds['earliest']['offset'] < bounds['last_stable_offset']['offset'], 'no committed sample available'
        sample = call('kafka_records_peek', allow_truncated=True, topic=a.topic, partition=partition,
                      start_offset=bounds['earliest']['offset'], max_records=5,
                      max_bytes=65536, include_value=False)
        assert sample['records'], 'empty record sample'
        assert all(not {'key','value','headers'}.intersection(r) for r in sample['records'])
        after = call('kafka_group_inspect', group=a.group, topics=[a.topic])
        assert not after['members'] and commits(before)==commits(after), 'test group offsets changed'
        missing = 'mcp-missing-readonly-'+uuid.uuid4().hex
        result, absent = envelope('kafka_topic_inspect', topic=missing)
        assert result.get('isError') and absent['status']=='query_failed', 'missing topic error hidden (allowlist must permit mcp-*)'
        assert not call('kafka_topics_list',query=missing)['topics'], 'missing topic auto-created'
        print(json.dumps(dict(result='PASS',version=init['serverInfo']['version'],
                              tools=len(listed),config_keys=len(configs[0]),
                              group_state=before['state'],offset_partitions=len(commits(before)),
                              sampled_records=len(sample['records']),payload_returned=False,
                              group_offsets_unchanged=True,missing_topic_not_created=True,cli_comparison=False)))
    finally:
        proc.stdin.close()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
        proc.stdout.close()


if __name__=='__main__':
    main()
