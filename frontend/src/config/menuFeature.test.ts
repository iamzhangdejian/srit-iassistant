import test from 'node:test'
import assert from 'node:assert/strict'
import { OEM_MENU_ALL, OEM_MENU_FEATURE, OEM_MENU_WHITELIST } from './oem'

// 功能位映射里的每个菜单都必须在白名单里，否则写了也不生效，
// 将来排查「为什么隐藏了没反应」会绕很大一圈。
test('feature-mapped menus are all inside the OEM whitelist', () => {
  const allowed = new Set(OEM_MENU_ALL)
  for (const path of Object.keys(OEM_MENU_FEATURE)) {
    assert.ok(allowed.has(path), `${path} is feature-gated but not in the OEM whitelist`)
    assert.ok(OEM_MENU_WHITELIST.includes(path), `${path} must be in OEM_MENU_WHITELIST`)
  }
})

// settings 与 logout 绝不能挂功能位：设置页要能换模型，退出登录要能走。
test('settings and logout are never feature-gated', () => {
  assert.equal(OEM_MENU_FEATURE.settings, undefined)
  assert.equal(OEM_MENU_FEATURE.logout, undefined)
})

// 映射值必须是后端认可的四档功能位之一。
test('feature values match the backend bitmap', () => {
  const valid = new Set(['kb', 'chat', 'ocr', 'office'])
  for (const [path, feature] of Object.entries(OEM_MENU_FEATURE)) {
    assert.ok(valid.has(feature), `${path} maps to unknown feature ${feature}`)
  }
})
