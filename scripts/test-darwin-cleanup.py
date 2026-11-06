import ast
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest
from unittest import mock


WORKFLOW = Path(__file__).resolve().parents[1] / '.github/workflows/platform-primitives.yml'


def cleanup_source():
    body = WORKFLOW.read_text().split('      - name: Always detach owned volumes and record cleanup\n', 1)[1]
    body = body.split('        run: |\n', 1)[1].split('      - name:', 1)[0]
    return textwrap.dedent(body)


def workflow_function(name):
    function = next(node for node in ast.parse(cleanup_source()).body
                    if isinstance(node, ast.FunctionDef) and node.name == name)
    namespace = {}
    exec(compile(ast.Module(body=[function], type_ignores=[]), str(WORKFLOW), 'exec'), namespace)
    return namespace[name]


class CleanupDiagnosticsTests(unittest.TestCase):
    def test_detach_messages_are_classified_without_returning_raw_output(self):
        classify = workflow_function('detach_diagnostic')
        for message, category in [(b'Resource busy', 'resource-busy'),
                                  (b'Invalid argument', 'invalid-argument'),
                                  (b'Permission denied', 'permission-denied'),
                                  (b'Operation not permitted', 'not-permitted'),
                                  (b'No such file or directory', 'not-found')]:
            for ending in [b'', b'\n']:
                with self.subTest(message=message, ending=ending):
                    output = b'hdiutil: detach failed - ' + message + ending
                    result = subprocess.CompletedProcess([], 16, b'fixture-secret-stdout', output)
                    self.assertEqual(classify(result), {
                        'class': category, 'exit': 16,
                        'stdout_bytes': 21, 'stderr_bytes': len(output),
                    })
        result = subprocess.CompletedProcess([], 16, b'fixture-secret', b'fixture-secret')
        self.assertEqual(classify(result)['class'], 'nonzero')
        self.assertNotIn('fixture-secret', json.dumps(classify(result)))
        result.stderr = b'hdiutil: detach failed - Resource busy\nfixture-secret'
        self.assertEqual(classify(result)['class'], 'nonzero')
        result.returncode = 0
        self.assertEqual(classify(result)['class'], 'success')

    def test_inspection_budgets_keep_the_exact_existing_boundaries(self):
        limit = workflow_function('cache_inspection_limit')
        self.assertEqual(limit(64, 200000, 10, 10), 'none')
        self.assertEqual(limit(65, 0, 0, 10), 'depth')
        self.assertEqual(limit(0, 200001, 0, 10), 'entries')
        self.assertEqual(limit(0, 0, 10.001, 10), 'time')

    def run_cleanup(self, exit_code, expire_cache=False):
        with tempfile.TemporaryDirectory(prefix='cognate-cleanup-test-') as temporary:
            root = Path(temporary).resolve()
            root.chmod(0o700)
            for name in ['home', 'config', 'data', 'cache', 'tmp', 'gopath', 'go-build',
                         'bin', 'evidence', 'sensitive', 'insensitive']:
                (root / name).mkdir(mode=0o700)
            for role in ['sensitive', 'insensitive']:
                (root / (role + '.sparseimage')).write_bytes(b'fixture-image')
            mounts = {}

            def is_mount(path):
                role = Path(path).name
                mounts[role] = mounts.get(role, 0) + 1
                return mounts[role] == 1

            result = subprocess.CompletedProcess([], exit_code, b'fixture-secret-stdout',
                                                 b'hdiutil: detach failed - Resource busy\n')
            output = io.StringIO()
            environment = {'COGNATE_BASELINE_ROOT': str(root), 'GOPATH': str(root / 'gopath'),
                           'GOMODCACHE': str(root / 'gopath/pkg/mod')}
            clock = mock.patch('time.monotonic', side_effect=[0, 11]) if expire_cache else contextlib.nullcontext()
            with mock.patch.dict(os.environ, environment), mock.patch('os.path.ismount', side_effect=is_mount), \
                    mock.patch('subprocess.run', return_value=result) as run, contextlib.redirect_stdout(output), clock:
                if exit_code or expire_cache:
                    with self.assertRaisesRegex(SystemExit, 'owned fixture cleanup incomplete'):
                        exec(compile(cleanup_source(), str(WORKFLOW), 'exec'), {})
                else:
                    exec(compile(cleanup_source(), str(WORKFLOW), 'exec'), {})
            self.assertEqual(run.call_count, 2)
            for call in run.call_args_list:
                self.assertEqual(call.args[0][:2], ['hdiutil', 'detach'])
                self.assertNotIn('-force', call.args[0])
            self.assertNotIn('fixture-secret', output.getvalue())
            receipt = json.loads((root / 'evidence/cleanup.json').read_text())
            self.assertEqual(receipt['failed'], bool(exit_code or expire_cache))
            self.assertEqual(receipt['auxiliary_failures'],
                             [{'role': 'gopath', 'class': 'cache-limit'}] if expire_cache else [])
            self.assertEqual((root / 'gopath').exists(), expire_cache)
            for role in ['sensitive', 'insensitive']:
                self.assertEqual(receipt['volumes'][role]['detach_exit'], exit_code)
                self.assertFalse(receipt['volumes'][role]['still_mounted'])
                self.assertEqual((root / (role + '.sparseimage')).exists(), bool(exit_code))
            return output.getvalue()

    def test_nonzero_detach_still_refuses_even_after_unmount(self):
        output = self.run_cleanup(16)
        events = [json.loads(line) for line in output.splitlines()]
        detach = [event for event in events if event['phase'] == 'detach']
        self.assertEqual([event['role'] for event in detach], ['sensitive', 'insensitive'])
        self.assertTrue(all(event['class'] == 'resource-busy' for event in detach))

    def test_successful_cleanup_keeps_the_existing_receipt_contract(self):
        self.run_cleanup(0)

    def test_cache_deadline_still_refuses_and_preserves_the_uninspected_cache(self):
        output = self.run_cleanup(0, expire_cache=True)
        events = [json.loads(line) for line in output.splitlines()]
        cache = next(event for event in events if event['phase'] == 'module-cache')
        self.assertEqual(cache, {'phase': 'module-cache', 'entries': 0, 'max_depth': 0,
                                 'limit': 'time', 'inspection_ms': 11000})


if __name__ == '__main__':
    unittest.main()
