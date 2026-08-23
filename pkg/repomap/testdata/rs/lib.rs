use std::collections::HashMap;

pub mod parser;

/// A parsed token.
pub struct Token {
    pub text: String,
}

pub trait Lexer {
    fn next_token(&mut self) -> Option<Token>;
}

pub enum Mode {
    Fast,
    Slow,
}

impl Token {
    pub fn new(text: String) -> Self {
        Token { text }
    }
}

pub fn tokenize(input: &str) -> Vec<Token> {
    input.split_whitespace().map(|s| Token::new(s.to_string())).collect()
}

fn private_helper(map: &HashMap<String, String>) -> usize {
    map.len()
}
