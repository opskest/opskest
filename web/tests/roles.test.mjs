import test from 'node:test'
import assert from 'node:assert/strict'
import { canWrite, isAdmin, ROLE_LABEL } from '../src/lib/roles.ts'

test('canWrite is true for admin and user only', () => {
  assert.equal(canWrite('admin'), true)
  assert.equal(canWrite({ role: 'user' }), true)
  assert.equal(canWrite({ role: 'viewer' }), false)
  assert.equal(canWrite({ role: 'disabled' }), false)
  assert.equal(canWrite(null), false)
})

test('isAdmin is true for admin only', () => {
  assert.equal(isAdmin({ role: 'admin' }), true)
  assert.equal(isAdmin('user'), false)
  assert.equal(isAdmin('viewer'), false)
})

test('every role has a label', () => {
  assert.equal(ROLE_LABEL.admin, 'Admin')
  assert.equal(ROLE_LABEL.user, 'User')
  assert.equal(ROLE_LABEL.viewer, 'Viewer')
})
