// Cut a docs archive: copy docs/content/docs/current/ as it was at a release
// tag into docs/content/docs/vX.Y/, record where it came from, and list it in
// the root meta.json. Usage: npm run docs:archive -- vX.Y.Z
//
// The copy comes from the tag's git tree, never the working tree, so the
// archive is the docs that shipped with that release rather than whatever the
// checkout holds today. scripts/docs-archive-config-test.sh holds the result to
// that promise afterwards.
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const SOURCE_PATH = "docs/content/docs/current";
const DOCS_DIR = "docs/content/docs";
const PROVENANCE = "docs/content/archive-provenance.json";
const TAG = /^v(\d+)\.(\d+)\.(\d+)$/u;
const LINE = /^v(\d+)\.(\d+)$/u;

class CutError extends Error {}

function fail(message) {
  throw new CutError(message);
}

function parseArgs(argv) {
  let root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  let tag;
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === "--root") {
      i += 1;
      if (!argv[i]) fail("--root needs a directory");
      root = path.resolve(argv[i]);
    } else if (argv[i].startsWith("-")) {
      fail(`unknown option ${argv[i]}`);
    } else if (tag === undefined) {
      tag = argv[i];
    } else {
      fail(`unexpected argument ${argv[i]}`);
    }
  }
  if (tag === undefined) fail("usage: npm run docs:archive -- vX.Y.Z");
  return { root, tag };
}

function readJson(file) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (error) {
    fail(`cannot read ${file} as JSON: ${error.message}`);
  }
}

function readJsonObject(file, label) {
  const value = readJson(file);
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    fail(`${label} must be a JSON object`);
  }
  return value;
}

function writeJson(file, value) {
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

// Newest line first, by numeric major then minor.
function byLineDescending(a, b) {
  const [, aMajor, aMinor] = LINE.exec(a);
  const [, bMajor, bMinor] = LINE.exec(b);
  return Number(bMajor) - Number(aMajor) || Number(bMinor) - Number(aMinor);
}

function cut({ root, tag }) {
  // -C does not override GIT_DIR or GIT_INDEX_FILE. Run from a hook or a
  // wrapper that exports them, git would read some other repository than root.
  const env = Object.fromEntries(
    Object.entries(process.env).filter(([name]) => !name.startsWith("GIT_")),
  );
  const git = (...args) =>
    execFileSync("git", ["-C", root, ...args], {
      encoding: "utf8",
      env,
      maxBuffer: 64 << 20,
    }).trim();
  const gitBuffer = (...args) =>
    execFileSync("git", ["-C", root, ...args], { env, maxBuffer: 64 << 20 });

  const match = TAG.exec(tag);
  if (!match) fail(`${tag} is not a release tag; expected vX.Y.Z`);
  const [, major, minor, patch] = match;
  const line = `v${major}.${minor}`;

  let commit;
  try {
    commit = git("rev-parse", "--verify", "--quiet", `refs/tags/${tag}^{commit}`);
  } catch {
    fail(`tag ${tag} does not exist here; fetch tags first`);
  }

  // The archive must be the end of its line, or the next patch's docs changes
  // would be missing from "the docs for v0.9".
  const laterPatches = git("tag", "-l", `v${major}.${minor}.*`)
    .split("\n")
    .filter((name) => TAG.test(name))
    .filter((name) => Number(TAG.exec(name)[3]) > Number(patch));
  if (laterPatches.length > 0) {
    const newest = laterPatches
      .sort((a, b) => Number(TAG.exec(a)[3]) - Number(TAG.exec(b)[3]))
      .at(-1);
    fail(`${tag} is not the last tag of ${line}; cut from ${newest} instead`);
  }

  const docsRoot = path.join(root, DOCS_DIR);
  const archiveDir = path.join(docsRoot, line);
  if (fs.existsSync(archiveDir))
    fail(`${DOCS_DIR}/${line}/ already exists; archives are immutable`);

  const currentMeta = readJsonObject(
    path.join(docsRoot, "current", "meta.json"),
    "current/meta.json",
  );
  if (!LINE.test(currentMeta.title ?? "")) {
    fail(
      `${DOCS_DIR}/current/meta.json title must be a line like v0.9, found ${currentMeta.title}`,
    );
  }
  if (currentMeta.title === line) {
    fail(`${line} is the line current/ documents; archive it only after current/ moves on`);
  }

  const provenancePath = path.join(root, PROVENANCE);
  const provenanceBefore = fs.existsSync(provenancePath)
    ? fs.readFileSync(provenancePath, "utf8")
    : undefined;
  const provenance =
    provenanceBefore === undefined ? {} : readJsonObject(provenancePath, PROVENANCE);
  if (provenance[line]) fail(`${PROVENANCE} already has an entry for ${line}`);

  // Every input is read and validated before the first write, so a bad file
  // cannot leave a half-done cut that the retry then refuses as immutable.
  const rootMetaPath = path.join(docsRoot, "meta.json");
  const rootMetaBefore = fs.readFileSync(rootMetaPath, "utf8");
  const rootMeta = readJsonObject(rootMetaPath, `${DOCS_DIR}/meta.json`);
  const archives = [
    ...fs
      .readdirSync(docsRoot, { withFileTypes: true })
      .filter((entry) => entry.isDirectory() && LINE.test(entry.name))
      .map((entry) => entry.name),
    line,
  ].sort(byLineDescending);

  // `current/` only exists in tags cut after docs moved under it. Older tags
  // have a flat docs/content/docs, which is not byte-identical to any archive
  // layout, so they are refused rather than converted.
  let sourceTree;
  try {
    sourceTree = git("rev-parse", "--verify", "--quiet", `${commit}:${SOURCE_PATH}`);
  } catch {
    fail(`${tag} has no ${SOURCE_PATH}; tags from before the current/ move cannot be archived`);
  }
  const entries = gitBuffer("ls-tree", "-r", "-z", commit, "--", SOURCE_PATH)
    .toString("utf8")
    .split("\0")
    .filter(Boolean)
    .map((row) => {
      const tab = row.indexOf("\t");
      const [mode, type, sha] = row.slice(0, tab).split(" ");
      return { mode, type, sha, rel: row.slice(tab + 1 + SOURCE_PATH.length + 1) };
    });
  const odd = entries.find((entry) => entry.type !== "blob" || entry.mode !== "100644");
  if (odd) fail(`${tag}:${SOURCE_PATH}/${odd.rel} is not a regular file (${odd.mode} ${odd.type})`);
  if (entries.length === 0) fail(`${tag}:${SOURCE_PATH} is empty`);

  const staging = fs.mkdtempSync(path.join(os.tmpdir(), "docs-archive-"));
  try {
    for (const entry of entries) {
      const target = path.join(staging, entry.rel);
      fs.mkdirSync(path.dirname(target), { recursive: true });
      fs.writeFileSync(target, gitBuffer("cat-file", "blob", entry.sha));
    }
    // Byte-identity check against the tree itself, without git's line-ending
    // or attribute filters, so a checkout setting cannot alter the copy.
    for (const entry of entries) {
      const written = execFileSync(
        "git",
        ["hash-object", "--no-filters", path.join(staging, entry.rel)],
        { encoding: "utf8", env },
      ).trim();
      if (written !== entry.sha) fail(`${entry.rel} did not copy byte-for-byte`);
    }
    // Writes start here. If any step fails, undo only what this run did.
    try {
      fs.cpSync(staging, archiveDir, { recursive: true, errorOnExist: true, force: false });

      provenance[line] = {
        sourceTag: tag,
        sourceCommit: commit,
        sourcePath: SOURCE_PATH,
        sourceTree,
      };
      const sortedProvenance = Object.fromEntries(
        Object.keys(provenance)
          .sort(byLineDescending)
          .map((key) => [key, provenance[key]]),
      );
      fs.mkdirSync(path.dirname(provenancePath), { recursive: true });
      writeJson(provenancePath, sortedProvenance);

      // current first, then archives newest first. Other keys in meta.json stay.
      writeJson(rootMetaPath, { ...rootMeta, pages: ["current", ...archives] });
    } catch (error) {
      // The cut refused up front if this directory existed, so whatever is
      // there now came from this run.
      fs.rmSync(archiveDir, { recursive: true, force: true });
      if (provenanceBefore === undefined) fs.rmSync(provenancePath, { force: true });
      else fs.writeFileSync(provenancePath, provenanceBefore);
      fs.writeFileSync(rootMetaPath, rootMetaBefore);
      throw error;
    }
  } finally {
    fs.rmSync(staging, { recursive: true, force: true });
  }

  return { line, commit, sourceTree, files: entries.length };
}

try {
  const result = cut(parseArgs(process.argv.slice(2)));
  console.log(
    `Archived ${result.files} files from ${result.commit.slice(0, 12)} as ${DOCS_DIR}/${result.line}/ (tree ${result.sourceTree}).`,
  );
  console.log(`Next: review the diff and commit docs/content/docs and ${PROVENANCE} together.`);
} catch (error) {
  if (!(error instanceof CutError)) throw error;
  console.error(`FAIL: ${error.message}`);
  process.exit(1);
}
