#!/usr/bin/env python3
"""Keep the Forgejo copy of Bloem CI in step with the GitHub workflow.

Bloem's CI is moving from GitHub Actions to Forgejo Actions. Until the move is done, both run: GitHub reads
.github/workflows and ignores .forgejo/workflows, and Forgejo reads only .forgejo/workflows once that directory
exists. The Forgejo copy is generated from the GitHub workflow by the substitutions below, so the two can't drift.

    python3 scripts/ci/compare-workflows.py           # exit 1 when a copy differs from what the GitHub file gives
    python3 scripts/ci/compare-workflows.py --write   # regenerate the copies after editing a GitHub workflow

Edit the GitHub workflow, never the Forgejo copy, then run --write and commit both. `make verify-local-gate` runs
the check, so Go contract checks fail on both forges when the copy is stale. The substitutions, in order:

  1. A header comment saying where the copy comes from.
  2. YAML anchors and aliases are expanded: Forgejo refuses a workflow whose alias names an anchor in another job.
  3. The triggers become pushes to main only. Shadow CI doubles the load on the runner nodes.
  4. "Prune old Go caches" goes: it prunes GitHub's Actions cache through GitHub's API, and the Forgejo cache
     server is pruned on its node.
  5. runs-on becomes the job's plain bloem-job-<kind> label, the kind named in the GitHub routing expression, and
     the routing comment above jobs: says so instead.
  6. services: become a first step that starts the same containers with docker run, ports published on 127.0.0.1
     only, and waits for their health checks, and a last step that removes them whatever happened. Host-mode
     runners start no services. ${{ job.services.<name>.id }} becomes the container's name, <name>.
  7. Right after the checkout, the checkout moves to /opt/actions-runner/_work/bloem-server/bloem-server, where the
     job VM's template warmed Go's caches, and the workspace becomes a link to it. Go's build cache keys a package
     by its directory, and the Forgejo runner gives every job a new random workspace. A step at the end of the job
     reports how many Go build cache entries the job added (misses) and read again (hits).
  8. actions/upload-artifact and download-artifact become Forgejo's forks; GitHub's v4 refuses a Forgejo server.
  9. The contract diff's fetch of this repository sends its token to the Forgejo server, not to github.com.

Workflows not listed in PORTED get no copy. Every Bloem workflow (bloem-*.yml) must be in PORTED or NOT_PORTED;
the other files are Silo's, which Forgejo never runs.

Python 3.9 or later, standard library only. PyYAML, when installed, also parses the result.
"""
import difflib
import os
import re
import shlex
import sys

GITHUB_DIR = '.github/workflows'
FORGEJO_DIR = '.forgejo/workflows'

PORTED = ('bloem-ci.yml',)

# Bloem's GitHub workflows that get no Forgejo copy, with the reason.
NOT_PORTED = {
    'bloem-image.yml': 'disabled; images are built only at deploy time (D5)',
    'bloem-release.yml': 'disabled; bloem-deploy tags the release itself',
    'upstream-sync.yml': 'disabled; the merge bot merges Silo',
    'upstream-sync-merge.yml': 'disabled with upstream-sync.yml',
}

DROPPED_JOBS = ('prune-go-caches',)

UPLOAD = 'https://code.forgejo.org/forgejo/upload-artifact@16871d9e8cfcf27ff31822cac382bbb5450f1e1e # v4'
DOWNLOAD = 'https://code.forgejo.org/forgejo/download-artifact@d8d0a99033603453ad2255e58720b460a0555e1e # v4'

# Where bloem-ci-runner's template checks bloem-server out to warm Go's caches (vm/profile-bloem.sh), which is
# also where the GitHub runner checks it out.
WARM_PATH = '/opt/actions-runner/_work/bloem-server/bloem-server'

ON = '''\
on:
  # Forgejo: shadow runs on pushes to main only (docs/bloem/ci.md, "The Forgejo copy").
  push:
    branches:
      - main
'''

ROUTING = '''\
# Forgejo: each job runs on the plain label bloem-job-<kind>, the job kind named in the GitHub copy's routing
# expression. bloem-ci-runner's Forgejo controller gives each one its own VM, sized as the GitHub controller sizes
# that kind.
'''

WARM_STEP = '''\
# Forgejo: build where the job VM's template warmed Go's caches. Go's build cache keys a package by its directory,
# and the runner gives every job a new random workspace, so the checkout moves to the path the template built at
# and the workspace becomes a link to it. Every later step, actions included, then works in that directory.
- name: Move the checkout to the warm build path
  run: |
    warm={warm}
    cd /
    mkdir -p "${{warm%/*}}"
    rm -rf "$warm"
    mv "$GITHUB_WORKSPACE" "$warm"
    ln -s "$warm" "$GITHUB_WORKSPACE"
    # Counted again in the last step: the entries a job adds are its build cache misses.
    cache=${{GOCACHE:-$HOME/.cache/go-build}}
    find "$cache" -type f -name '*-a' 2> /dev/null | wc -l > "$RUNNER_TEMP/go-cache-before"
    touch "$RUNNER_TEMP/go-cache-start"
'''

REPORT_STEP = '''\
# Forgejo: how much of the template's warm Go build cache this job used. Go touches an entry older than an hour
# when it reads it, and the template's entries are older, so entries newer than the start that the job did not add
# are hits.
- name: Report the Go build cache
  if: always()
  run: |
    cache=${GOCACHE:-$HOME/.cache/go-build}
    if command -v go > /dev/null; then echo "Go builds in $(dirname "$(go env GOMOD)")"; fi
    before=$(cat "$RUNNER_TEMP/go-cache-before" 2> /dev/null || echo 0)
    after=$(find "$cache" -type f -name '*-a' 2> /dev/null | wc -l)
    newer=$(find "$cache" -type f -name '*-a' -newer "$RUNNER_TEMP/go-cache-start" 2> /dev/null | wc -l)
    echo "GOCACHE $cache: $before action entries at the start, $after at the end"
    echo "misses (entries added): $((after - before)), hits (older entries read): $((newer - (after - before)))"
'''

START_COMMENT = '''\
# Forgejo: a host-mode runner starts no services, so the job runs the same containers itself, publishes their
# ports on 127.0.0.1 only, and waits for their health checks, as GitHub does before the first step.
'''

WAIT = '''\
for name in {names}; do
  for _ in $(seq 300); do
    status=$(docker inspect -f '{{{{.State.Health.Status}}}}' "$name")
    [ "$status" = starting ] || break
    sleep 1
  done
  if [ "$status" != healthy ]; then
    docker logs "$name"
    echo "::error::Service container $name is $status."
    exit 1
  fi
done
'''

ITEM_ANCHOR = re.compile(r'^(\s*)- &([\w-]+)\s*$')
KEY_ANCHOR = re.compile(r'^(\s*)([\w-]+): &([\w-]+)\s*$')
ITEM_ALIAS = re.compile(r'^(\s*)- \*([\w-]+)\s*$')
KEY_ALIAS = re.compile(r'^(\s*)([\w-]+): \*([\w-]+)\s*$')
LEFTOVER = re.compile(r'^\s*(- |[\w-]+: )[&*][\w-]+(\s|$)')


class Unported(Exception):
    """The GitHub workflow uses something this script has no Forgejo substitution for."""


def indent_of(line):
    return len(line) - len(line.lstrip(' '))


def block_end(lines, start, ind):
    """Index just past the node that starts at lines[start] with indentation ind, trailing blank lines excluded."""
    j = start + 1
    while j < len(lines) and (not lines[j].strip() or indent_of(lines[j]) > ind):
        j += 1
    while j > start + 1 and not lines[j - 1].strip():
        j -= 1
    return j


def shift(lines, by):
    return [(' ' * (indent_of(l) + by) + l.lstrip(' ')) if l.strip() else '' for l in lines]


def indented(text, ind):
    return [(' ' * ind + l) if l else '' for l in text.splitlines()]


def expand_anchors(lines):
    anchors = {}
    for i, line in enumerate(lines):
        m = ITEM_ANCHOR.match(line)
        if m:
            ind = len(m.group(1))
            body = lines[i + 1:block_end(lines, i, ind)]
            if not body or indent_of(body[0]) != ind + 2 or body[0].lstrip().startswith('#'):
                raise Unported(f'anchor &{m.group(2)}: its item has to start on the next line')
            anchors[m.group(2)] = ('item', ind, body)
            continue
        m = KEY_ANCHOR.match(line)
        if m:
            ind = len(m.group(1))
            anchors[m.group(3)] = ('key', ind, lines[i + 1:block_end(lines, i, ind)])
    out, skip = [], False
    for line in lines:
        if skip:  # the first line of an anchored item, already joined to its "- "
            skip = False
            continue
        m = ITEM_ANCHOR.match(line)
        if m:
            _, _, body = anchors[m.group(2)]
            out.append(f'{m.group(1)}- {body[0].strip()}')
            skip = True
            continue
        m = KEY_ANCHOR.match(line)
        if m:
            out.append(f'{m.group(1)}{m.group(2)}:')
            continue
        m = ITEM_ALIAS.match(line) or KEY_ALIAS.match(line)
        if m:
            name = m.group(m.lastindex)
            if name not in anchors:
                raise Unported(f'alias *{name} names no anchor')
            kind, ind, body = anchors[name]
            by = len(m.group(1)) - ind
            if m.re is ITEM_ALIAS:
                if kind != 'item':
                    raise Unported(f'alias *{name} is a list item, but its anchor is not')
                out.append(f'{m.group(1)}- {body[0].strip()}')
                out += shift(body[1:], by)
            else:
                if kind != 'key':
                    raise Unported(f'alias *{name} is a mapping value, but its anchor is a list item')
                out.append(f'{m.group(1)}{m.group(2)}:')
                out += shift(body, by)
            continue
        out.append(line)
    return out


def replace_on(lines):
    start = lines.index('on:')
    end = start + 1
    while end < len(lines) and not (lines[end].strip() and indent_of(lines[end]) == 0):
        end += 1
    while not lines[end - 1].strip():
        end -= 1
    if not any(re.match(r'^\s+push:', l) for l in lines[start:end]):
        raise Unported('the workflow does not run on push')
    return lines[:start] + ON.splitlines() + lines[end:]


def split_jobs(lines):
    """(lines up to and including "jobs:", [(job id, its lines, leading comments included)])."""
    start = lines.index('jobs:') + 1
    for line in lines[start:]:
        if line.strip() and indent_of(line) == 0:
            raise Unported('jobs: has to be the last top-level key')
    heads = [i for i in range(start, len(lines)) if re.match(r'^  [\w-]+:\s*$', lines[i])]
    starts = []
    for h in heads:
        s = h
        while s > start and lines[s - 1].startswith('  #'):
            s -= 1
        starts.append(s)
    if not heads or any(l.strip() for l in lines[start:starts[0]]):
        raise Unported('something other than a job sits under jobs:')
    jobs = []
    for n, (h, s) in enumerate(zip(heads, starts)):
        e = starts[n + 1] if n + 1 < len(starts) else len(lines)
        jobs.append((lines[h].strip().rstrip(':'), lines[s:e]))
    return lines[:start], jobs


def replace_routing_comment(prefix):
    end = len(prefix) - 1  # "jobs:"
    start = end
    while start > 0 and prefix[start - 1].startswith('#'):
        start -= 1
    comment = '\n'.join(prefix[start:end])
    if 'BLOEM_SELF_HOSTED' not in comment:
        raise Unported('the comment above jobs: no longer explains the runner routing')
    return prefix[:start] + ROUTING.splitlines() + prefix[end:]


def parse_services(block, base):
    services, pending, i = [], [], 0
    while i < len(block):
        line, s = block[i], block[i].strip()
        if not s:
            i += 1
            continue
        if s.startswith('#') and indent_of(line) == base:
            pending.append(s.lstrip('#').strip())
            i += 1
            continue
        m = re.match(r'^([\w-]+):$', s)
        if indent_of(line) != base or not m:
            raise Unported(f'unexpected line in services: {s}')
        svc = {'name': m.group(1), 'comments': pending, 'image': '', 'env': [], 'ports': [], 'options': []}
        pending = []
        i += 1
        while i < len(block) and (not block[i].strip() or indent_of(block[i]) > base):
            s = block[i].strip()
            if not s:
                i += 1
                continue
            if indent_of(block[i]) != base + 2 or s.startswith('#'):
                raise Unported(f'unexpected line in service {svc["name"]}: {s}')
            key, _, value = s.partition(':')
            value = value.strip()
            i += 1
            sub = []
            while i < len(block) and (not block[i].strip() or indent_of(block[i]) > base + 2):
                if block[i].strip():
                    sub.append(block[i].strip())
                i += 1
            if '${{' in value or any('${{' in x for x in sub):
                raise Unported(f'service {svc["name"]} uses an expression')
            if key == 'image' and value and not sub:
                svc['image'] = value
            elif key == 'env' and not value:
                for x in sub:
                    k, _, v = x.partition(':')
                    v = v.strip()
                    if len(v) > 1 and v[0] == v[-1] and v[0] in '"\'':
                        v = v[1:-1]
                    svc['env'].append(f'{k.strip()}={v}')
            elif key == 'ports' and not value:
                for x in sub:
                    port = x[2:].strip().strip('"\'') if x.startswith('- ') else ''
                    if not re.fullmatch(r'\d+:\d+', port):
                        raise Unported(f'service {svc["name"]}: port {x}')
                    svc['ports'].append(port)
            elif key == 'options' and value in ('>-', '>'):
                svc['options'] = sub
            else:
                raise Unported(f'service {svc["name"]}: no Forgejo substitution for "{s}"')
        if not svc['image'] or not any(o.startswith('--health-cmd') for o in svc['options']):
            raise Unported(f'service {svc["name"]} needs an image and a --health-cmd')
        services.append(svc)
    return services


def service_steps(services, ind):
    script = []
    for svc in services:
        script += [f'# {c}' for c in svc['comments']]
        args = [f'-p 127.0.0.1:{p}' for p in svc['ports']]
        args += [f'-e {shlex.quote(e)}' for e in svc['env']] + svc['options'] + [svc['image']]
        script.append(f'docker run -d --name {svc["name"]} \\')
        script += [f'  {a} \\' for a in args[:-1]] + [f'  {args[-1]}']
    names = ' '.join(s['name'] for s in services)
    script += WAIT.format(names=names).splitlines()
    start = indented(START_COMMENT, ind) + [f'{" " * ind}- name: Start the service containers',
                                            f'{" " * ind}  run: |'] + indented('\n'.join(script), ind + 4)
    stop = [f'{" " * ind}- name: Stop the service containers', f'{" " * ind}  if: always()',
            f'{" " * ind}  run: docker rm -f {names}']
    return start, stop


def step_starts(job, first):
    return [i for i in range(first, len(job)) if job[i].startswith('      - ')]


def port_job(name, job):
    runs_on = [i for i, l in enumerate(job) if l.startswith('    runs-on:')]
    if len(runs_on) != 1:
        raise Unported(f'job {name}: one runs-on expected')
    kinds = re.findall(r'"bloem-job-([\w-]+)"', job[runs_on[0]])
    if len(kinds) != 1:
        raise Unported(f'job {name}: its runs-on names no bloem-job-<kind> label')
    job[runs_on[0]] = f'    runs-on: bloem-job-{kinds[0]}'

    start = stop = None
    if '    services:' in job:
        s = job.index('    services:')
        e = block_end(job, s, 4)
        services = parse_services(job[s + 1:e], 6)
        start, stop = service_steps(services, 6)
        del job[s:e]
        for svc in services:
            job = [l.replace(f'${{{{ job.services.{svc["name"]}.id }}}}', svc['name']) for l in job]
    if any('job.services.' in l for l in job):
        raise Unported(f'job {name} reads a service it does not declare')

    if '    steps:' not in job:
        raise Unported(f'job {name} has no steps')
    first = job.index('    steps:') + 1
    if any(l.strip() and indent_of(l) <= 4 for l in job[first:]):
        raise Unported(f'job {name}: steps: has to be its last key')
    checkouts = [i for i in step_starts(job, first) if any(
        'uses: actions/checkout@' in l for l in job[i:block_end(job, i, 6)])]
    if len(checkouts) > 1:
        raise Unported(f'job {name} checks out more than once')
    end = max(i for i, l in enumerate(job) if l.strip()) + 1
    tail = []
    if checkouts:
        at = block_end(job, checkouts[0], 6)
        job[at:at] = [''] + indented(WARM_STEP.format(warm=WARM_PATH), 6)
        end = max(i for i, l in enumerate(job) if l.strip()) + 1
        tail += [''] + indented(REPORT_STEP, 6)
    if stop:
        tail += [''] + stop
    job[end:end] = tail
    if start:
        job[first:first] = start + ['']
    return job


def forgejo_copy(name, text):
    lines = text.splitlines()
    lines = expand_anchors(lines)
    lines = replace_on(lines)
    lines = [re.sub(r'uses: actions/upload-artifact@\S+(?: # \S+)?\s*$', 'uses: ' + UPLOAD, l) for l in lines]
    lines = [re.sub(r'uses: actions/download-artifact@\S+(?: # \S+)?\s*$', 'uses: ' + DOWNLOAD, l) for l in lines]
    lines = [l.replace('http.https://github.com/.extraheader', 'http.${GITHUB_SERVER_URL}/.extraheader')
             for l in lines]
    prefix, jobs = split_jobs(lines)
    lines = replace_routing_comment(prefix)
    for job_id, job in jobs:
        if job_id not in DROPPED_JOBS:
            lines += port_job(job_id, job)
    while lines and not lines[-1].strip():
        lines.pop()
    header = [f'# Forgejo copy of {GITHUB_DIR}/{name}, generated by scripts/ci/compare-workflows.py --write.',
              "# Edit the GitHub workflow and regenerate; the script's docstring lists every difference."]
    out = '\n'.join(header + lines) + '\n'
    check_copy(name, out)
    return out


def check_copy(name, text):
    lines = text.splitlines()
    for line in lines:
        if LEFTOVER.match(line):
            raise Unported(f'{name}: an anchor or alias is left: {line.strip()}')
        if re.match(r'^\s*runs-on:', line) and not re.fullmatch(r'    runs-on: bloem-job-[\w-]+', line):
            raise Unported(f'{name}: runs-on is not a plain bloem-job label: {line.strip()}')
        if re.match(r'^    services:', line) or 'job.services.' in line:
            raise Unported(f'{name}: a service is left: {line.strip()}')
        if re.search(r'uses: actions/(upload|download)-artifact@', line):
            raise Unported(f"{name}: GitHub's artifact action is left")
    try:
        import yaml
    except ImportError:
        return
    doc = yaml.load(text, Loader=yaml.BaseLoader)
    if sorted(doc) != ['concurrency', 'env', 'jobs', 'name', 'on', 'permissions'] or not doc['jobs']:
        raise Unported(f'{name}: the copy does not parse as the workflow it should be')


def main(argv):
    write = argv[1:] == ['--write']
    if argv[1:] not in ([], ['--write']):
        print(__doc__.strip())
        return 2
    os.chdir(os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '..'))
    names = sorted(n for n in os.listdir(GITHUB_DIR) if n.endswith(('.yml', '.yaml')))
    have = sorted(n for n in os.listdir(FORGEJO_DIR) if n.endswith(('.yml', '.yaml'))) if os.path.isdir(FORGEJO_DIR) else []
    bad = 0
    for n in names:
        if n.startswith('bloem-') and n not in PORTED and n not in NOT_PORTED:
            print(f'{GITHUB_DIR}/{n}: a Bloem workflow without a Forgejo copy. Add it to PORTED or NOT_PORTED in '
                  'scripts/ci/compare-workflows.py.')
            bad += 1
    for n in have:
        if n not in PORTED:
            print(f'{FORGEJO_DIR}/{n}: not generated by this script (PORTED lists the copies)')
            bad += 1
    for n in PORTED:
        source = os.path.join(GITHUB_DIR, n)
        if not os.path.exists(source):
            print(f'{source}: missing, but listed in PORTED')
            bad += 1
            continue
        with open(source, encoding='utf-8') as f:
            try:
                want = forgejo_copy(n, f.read())
            except Unported as e:
                print(f'{source}: no Forgejo copy: {e}. Teach scripts/ci/compare-workflows.py the substitution.')
                bad += 1
                continue
        path = os.path.join(FORGEJO_DIR, n)
        got = open(path, encoding='utf-8').read() if os.path.exists(path) else None
        if got == want:
            continue
        if write:
            os.makedirs(FORGEJO_DIR, exist_ok=True)
            with open(path, 'w', encoding='utf-8') as f:
                f.write(want)
            print(f'wrote {path}')
            continue
        bad += 1
        if got is None:
            print(f'{path} is missing. Run: python3 scripts/ci/compare-workflows.py --write')
        else:
            print(f'{path} differs from what {source} gives. Edit the GitHub workflow, then run '
                  'python3 scripts/ci/compare-workflows.py --write.')
            sys.stdout.writelines(difflib.unified_diff(want.splitlines(True), got.splitlines(True),
                                                       f'expected/{path}', path))
    if bad:
        return 1
    if not write:
        print(f'{len(PORTED)} Forgejo workflow copy matches {GITHUB_DIR}.')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
