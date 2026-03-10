#!/usr/bin/env node
import { spawnSync } from 'node:child_process'

const branch = (process.argv[2] || '').trim()
const message = (process.argv[3] || '').trim()

if (!branch || !message) {
  console.error('Usage: make commit branch=feature/name message="commit message"')
  process.exit(1)
}

function runGit(args, options = {}) {
  const result = spawnSync('git', args, {
    stdio: options.capture ? 'pipe' : 'inherit',
    encoding: 'utf8',
  })
  if (result.error) {
    console.error(result.error.message)
    process.exit(1)
  }
  if (result.status !== 0) {
    process.exit(result.status ?? 1)
  }
  return (result.stdout || '').trim()
}

const current = runGit(['rev-parse', '--abbrev-ref', 'HEAD'], { capture: true })
if (current === 'HEAD') {
  console.error('Detached HEAD is not supported. Switch to a branch first.')
  process.exit(1)
}

if (current === 'main' || current === 'master') {
  const branchExists = spawnSync('git', ['show-ref', '--verify', '--quiet', `refs/heads/${branch}`], { stdio: 'ignore' }).status === 0
  runGit(branchExists ? ['switch', branch] : ['switch', '-c', branch])
} else if (current !== branch) {
  console.error(`Current branch is '${current}'. Use branch=${current} or switch manually.`)
  process.exit(1)
}

const worktreeClean = spawnSync('git', ['diff', '--quiet'], { stdio: 'ignore' }).status === 0
const stagedClean = spawnSync('git', ['diff', '--cached', '--quiet'], { stdio: 'ignore' }).status === 0
if (worktreeClean && stagedClean) {
  console.error('No changes to commit.')
  process.exit(1)
}

runGit(['add', '-A'])

const nothingStaged = spawnSync('git', ['diff', '--cached', '--quiet'], { stdio: 'ignore' }).status === 0
if (nothingStaged) {
  console.error('Nothing staged after git add.')
  process.exit(1)
}

runGit(['commit', '-m', message])
runGit(['push', '-u', 'origin', branch])

console.log('Next: open a PR to main and wait for CI + approval before merge.')
