package repomap

import (
	"sort"
	"strings"
	"testing"
)

// symKey renders a symbol as "kind:Receiver.Name" so a fixture assertion reads
// as the fact it is checking rather than as an index into a slice.
func symKey(s Symbol) string {
	if s.Receiver != "" {
		return s.Kind + ":" + s.Receiver + "." + s.Name
	}
	return s.Kind + ":" + s.Name
}

func extractKeys(t *testing.T, path, src string) map[string]bool {
	t.Helper()
	f := ExtractSource(path, src)
	out := map[string]bool{}
	for _, s := range f.Symbols {
		out[symKey(s)] = true
	}
	return out
}

func wantSymbols(t *testing.T, path, src string, want ...string) {
	t.Helper()
	got := extractKeys(t, path, src)
	var missing []string
	for _, w := range want {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		have := make([]string, 0, len(got))
		for k := range got {
			have = append(have, k)
		}
		sort.Strings(have)
		t.Errorf("%s: missing %v\n  extracted: %s", path, missing, strings.Join(have, ", "))
	}
}

func TestExtractGoGroupedDeclarations(t *testing.T) {
	// A package whose whole exported surface is a const block used to
	// contribute zero symbols to the repo map.
	src := `package cfg

import "fmt"

const (
	MaxRetries = 3
	timeout    = 30
)

var (
	ErrClosed = fmt.Errorf("closed")
)

type (
	Handler func(int) error
	Store   interface{ Get(string) string }
)

func (s *Server[T]) Handle(ctx context.Context, req *Request) error { return nil }

func New[T any](opts ...Option) *Server[T] { return nil }
`
	wantSymbols(t, "cfg.go", src,
		"const:MaxRetries", "const:timeout", "var:ErrClosed",
		"type:Handler", "interface:Store",
		"method:Server.Handle", "func:New")
}

func TestExtractTypeScriptShapes(t *testing.T) {
	src := `import { z } from "zod";
export type Result<T, E = Error> = { ok: true } | { ok: false };
export interface Props<T extends object> { items: T[] }
export const useThing = <T,>(x: T): T => x;
export const Card: React.FC<Props<Foo>> = ({ items }) => null;
export default class Widget<T> extends Base<T> implements Disposable {
  private secret = 1;
  async load(id: string): Promise<void> {}
  static create(): Widget<any> { return new Widget(); }
}
export enum Color { Red }
export const enum Flag { A }
export async function fetchAll<T>(u: string): Promise<T[]> { return []; }
export const LIMIT = 42;
`
	wantSymbols(t, "widget.tsx", src,
		"type:Result", "interface:Props",
		"func:useThing", // generic arrow — the <T,> form used throughout .tsx
		"func:Card",     // typed const component
		"class:Widget",
		"method:Widget.load", "method:Widget.create", // class members
		"type:Color", "type:Flag", "func:fetchAll", "const:LIMIT")
}

func TestExtractRustImplBlocks(t *testing.T) {
	src := `use std::collections::HashMap;
pub struct Config<T: Send> { pub name: String }
pub trait Store: Send { fn get(&self) -> Option<String>; }
impl<T: Send> Config<T> {
    pub fn new(name: &str) -> Self { todo!() }
    fn helper(&self) {}
}
impl Store for Config<u8> {
    fn get(&self) -> Option<String> { None }
}
pub type Alias = HashMap<String, u32>;
macro_rules! my_macro { () => {} }
pub const MAX: usize = 10;
pub static NAME: &str = "x";
`
	wantSymbols(t, "config.rs", src,
		"type:Config", "interface:Store",
		"class:Config",       // impl<T: Send> Config<T> — generics hug `impl`
		"class:Store.Config", // impl Store for Config — the TYPE, trait as recv
		"method:Config.new", "method:Config.helper", "method:Config.get",
		"type:Alias", "func:my_macro", "const:MAX", "var:NAME")
}

func TestExtractJavaMembers(t *testing.T) {
	src := `package com.example.app;
import java.util.List;

@Service
public class OrderService extends Base implements Runnable {
    private static final int MAX = 10;
    public OrderService(Repo repo) {}
    @Override
    public List<Order> findAll(String q) throws SQLException { return null; }
    private <T extends Comparable<T>> T max(List<T> in) { return null; }
    public static class Inner {}
}
`
	f := ExtractSource("OrderService.java", src)
	if f.Package != "com.example.app" {
		t.Errorf("package = %q", f.Package)
	}
	wantSymbols(t, "OrderService.java", src,
		"class:OrderService",
		"const:OrderService.MAX",
		"method:OrderService.OrderService", // constructor: no return type
		"method:OrderService.findAll",
		"method:OrderService.max",
		"class:OrderService.Inner")
}

func TestExtractPythonScoping(t *testing.T) {
	src := `from __future__ import annotations
from .models import User

MAX_RETRIES: int = 3

class Order:
    def save(self) -> None: ...

    @staticmethod
    def build(x: int) -> "Order": ...

def fetch_all(q: str) -> list[Order]:
    def inner():
        pass
    return []
`
	f := ExtractSource("orders.py", src)
	if !contains(f.Imports, ".models") {
		t.Errorf("relative import dropped: %v", f.Imports)
	}
	wantSymbols(t, "orders.py", src,
		"const:MAX_RETRIES", "class:Order",
		"method:Order.save", "method:Order.build", "func:fetch_all")
	// A def nested inside a FUNCTION is not part of the module's API.
	if extractKeys(t, "orders.py", src)["method:inner"] {
		t.Error("nested helper reported as a method")
	}
}

func TestExtractCSharp(t *testing.T) {
	src := `namespace App.Services;
using System.Text.Json;

[ApiController]
public sealed class OrderService : IOrderService
{
    public string Name { get; init; }
    public OrderService(IRepo repo) { }
    public async Task<List<Order>> FindAllAsync(string q) { return null; }
}
public record Point(int X, int Y);
public interface IOrderService { }
`
	f := ExtractSource("OrderService.cs", src)
	if f.Package != "App.Services" {
		t.Errorf("namespace = %q", f.Package)
	}
	wantSymbols(t, "OrderService.cs", src,
		"class:OrderService",
		"var:OrderService.Name",
		"method:OrderService.OrderService",
		"method:OrderService.FindAllAsync",
		"class:Point", "interface:IOrderService")
}

func TestExtractKotlin(t *testing.T) {
	src := `package com.example
import kotlinx.coroutines.flow.Flow

@Serializable
data class User(val id: Long, val name: String)
sealed interface Result
object Registry { fun register(x: String) {} }
class Repo(private val db: Db) {
    suspend fun findAll(q: String): Flow<User> = TODO()
    private fun helper() {}
}
typealias Handler = (String) -> Unit
const val MAX = 10
fun topLevel(): Int = 1
`
	wantSymbols(t, "Repo.kt", src,
		"class:User", "interface:Result", "class:Registry", "class:Repo",
		"method:Repo.findAll", "method:Repo.helper",
		"type:Handler", "const:MAX", "func:topLevel")
	// `class Repo(private val db: Db)` is a PUBLIC class with a private ctor
	// parameter — a naive substring search on the line calls it private.
	for _, s := range ExtractSource("Repo.kt", src).Symbols {
		if s.Name == "Repo" && !s.Exported {
			t.Error("Repo marked unexported by its constructor parameter")
		}
	}
}

func TestExtractSwift(t *testing.T) {
	src := `import Foundation
public protocol Store { func get(_ k: String) -> String? }
public struct Config: Codable {
    public let name: String
    public init(name: String) { self.name = name }
    public func describe() -> String { return name }
}
final class Engine {
    func run() async throws {}
}
public typealias Handler = (String) -> Void
`
	wantSymbols(t, "Config.swift", src,
		"interface:Store", "type:Config",
		"const:Config.name", "method:Config.init", "method:Config.describe",
		"class:Engine", "method:Engine.run", "type:Handler")
}

func TestExtractRuby(t *testing.T) {
	src := `require 'json'
require_relative 'helper'
module Billing
  MAX_RETRIES = 3
  class Invoice < Base
    attr_accessor :total, :currency
    def initialize(total)
      @total = total
    end
    def self.build(x)
    end
    private
    def internal
    end
  end
end
def top_level_helper(x)
end
`
	wantSymbols(t, "invoice.rb", src,
		"type:Billing", "const:Billing.MAX_RETRIES", "class:Billing.Invoice",
		"var:Invoice.total", "var:Invoice.currency",
		"method:Invoice.initialize", "method:Invoice.build",
		"func:top_level_helper") // `end` must close the class scope
	for _, s := range ExtractSource("invoice.rb", src).Symbols {
		if s.Name == "internal" && s.Exported {
			t.Error("`private` section marker ignored")
		}
	}
}

func TestExtractPHP(t *testing.T) {
	src := `<?php
namespace App\Service;
use App\Repo\OrderRepo;

final class OrderService implements OrderServiceInterface
{
    public const MAX = 10;
    private OrderRepo $repo;
    public function __construct(OrderRepo $repo) { $this->repo = $repo; }
    public function findAll(string $q): array { return []; }
}
trait Loggable { public function log(string $m): void {} }
function top_level(int $x): int { return $x; }
`
	f := ExtractSource("OrderService.php", src)
	if f.Package != `App\Service` {
		t.Errorf("namespace = %q", f.Package)
	}
	wantSymbols(t, "OrderService.php", src,
		"class:OrderService",
		"const:OrderService.MAX", "var:OrderService.repo",
		"method:OrderService.__construct", "method:OrderService.findAll",
		"class:Loggable",
		"func:top_level") // the one-line trait body must not swallow it
}

func TestExtractCFamilyAndShell(t *testing.T) {
	cpp := `#include <vector>
#define MAX_SIZE 1024
namespace app {
class Engine {
public:
    Engine(int n);
    int run(const std::string& q);
};
struct Point { int x; };
}
int main(int argc, char** argv) { return 0; }
`
	wantSymbols(t, "engine.cpp", cpp,
		"const:MAX_SIZE", "type:Engine",
		"method:Engine.Engine", "method:Engine.run",
		"type:Point", "func:main")

	sh := `#!/usr/bin/env bash
source ./lib/common.sh
build_all() {
  echo hi
}
function deploy {
  echo no
}
`
	wantSymbols(t, "build.sh", sh, "func:build_all", "func:deploy")
}

func TestLanguagesCoverEveryPack(t *testing.T) {
	// Every language with a bundled quality pack should also have a symbol
	// extractor, or its repo map is a list of filenames.
	want := []string{
		"go", "python", "typescript", "javascript", "rust", "java",
		"csharp", "kotlin", "swift", "ruby", "php", "cpp", "shell",
	}
	got := map[string]bool{}
	for _, l := range Languages() {
		got[l] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("no extractor for %q", w)
		}
	}
	for ext, lang := range map[string]string{
		".cs": "csharp", ".kt": "kotlin", ".swift": "swift", ".rb": "ruby",
		".php": "php", ".hpp": "cpp", ".sh": "shell", ".tsx": "typescript",
	} {
		if LangForPath("x"+ext) != lang {
			t.Errorf("LangForPath(%q) = %q, want %q", ext, LangForPath("x"+ext), lang)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
