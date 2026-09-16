#!/usr/bin/env node
/**
 * Conventions guard: branch names are <type>/<kebab>, commit subjects follow
 * Conventional Commits v1.0.0 (subject <= 100 chars; GitHub-generated
 * `Revert "..."` commits allowed; merge commits are not checked).
 *
 * CI is PR-only and sets BRANCH_NAME and COMMIT_RANGE. Local defaults are the
 * current branch and origin/main..HEAD. --branch <name> and --subject <text>
 * validate a single input.
 */
import { execFileSync } from 'node:child_process';

const types = [
  'feat',
  'fix',
  'chore',
  'docs',
  'refactor',
  'perf',
  'test',
  'build',
  'ci',
  'style',
  'revert',
];
const branchPattern = new RegExp(`^(${types.join('|')})/[a-z0-9]+(-[a-z0-9]+)*$`);
const subjectPattern = new RegExp(`^(${types.join('|')})(\\([a-z0-9-]+\\))?!?: .+$`);
const maxSubject = 100;

const branchProblem = (name) =>
  branchPattern.test(name)
    ? null
    : `branch "${name}" — expected <type>/<kebab-name> with type in ${types.join('|')}`;

const subjectProblem = (subject) => {
  if (/^Revert ".+"$/.test(subject)) return null;
  if (!subjectPattern.test(subject)) {
    return `commit "${subject}" — expected <type>(scope)?!?: description (Conventional Commits v1.0.0)`;
  }
  if (subject.length > maxSubject) {
    return `commit "${subject.slice(0, 40)}…" — subject is ${subject.length} chars, max ${maxSubject}`;
  }
  return null;
};

const git = (args) => execFileSync('git', args, { encoding: 'utf8' }).trim();
const args = process.argv.slice(2);
const flag = (name) => {
  const index = args.indexOf(name);
  return index >= 0 && index + 1 < args.length ? args[index + 1] : null;
};

const problems = [];
const onlyBranch = flag('--branch');
const onlySubject = flag('--subject');

if (onlyBranch !== null || onlySubject !== null) {
  if (onlyBranch !== null) {
    const problem = branchProblem(onlyBranch);
    if (problem) problems.push(problem);
  }
  if (onlySubject !== null) {
    const problem = subjectProblem(onlySubject);
    if (problem) problems.push(problem);
  }
} else {
  const branch = process.env.BRANCH_NAME || git(['rev-parse', '--abbrev-ref', 'HEAD']);
  if (branch !== 'main') {
    const problem = branchProblem(branch);
    if (problem) problems.push(problem);
  }

  const range = process.env.COMMIT_RANGE || 'origin/main..HEAD';
  const log = git(['log', '--no-merges', '--format=%s', range]);
  for (const subject of log.split(/\r?\n/).filter(Boolean)) {
    const problem = subjectProblem(subject);
    if (problem) problems.push(problem);
  }
}

if (problems.length > 0) {
  console.error('check:conventions FAILED — Conventional Commits v1.0.0 + <type>/<kebab> branch names\n');
  for (const problem of problems) console.error(problem);
  process.exit(1);
}
console.log('check:conventions OK');
