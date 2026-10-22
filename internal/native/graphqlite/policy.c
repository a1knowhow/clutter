#include <string.h>
#include "parser/cypher_parser.h"
static int node(ast_node*,int);
static int list(ast_list*l,int depth){if(!l)return 1;for(int i=0;i<l->count;i++)if(!node(l->items[i],depth+1))return 0;return 1;}
static int node(ast_node*n,int depth){if(!n)return 1;if(depth>128)return 0;
#define N(x) node((x),depth+1)
#define L(x) list((x),depth+1)
 switch(n->type){
 case AST_NODE_QUERY:case AST_NODE_SINGLE_QUERY:{cypher_query*q=(void*)n;return !q->explain&&!q->profile&&L(q->clauses);}
 case AST_NODE_UNION:{cypher_union*q=(void*)n;return N(q->left)&&N(q->right);}
 case AST_NODE_MATCH:{cypher_match*q=(void*)n;return !q->from_graph&&L(q->pattern)&&N(q->where);}
 case AST_NODE_RETURN:{cypher_return*q=(void*)n;return L(q->items)&&L(q->order_by)&&N(q->skip)&&N(q->limit);}
 case AST_NODE_WITH:{cypher_with*q=(void*)n;return L(q->items)&&L(q->order_by)&&N(q->skip)&&N(q->limit)&&N(q->where);}
 case AST_NODE_WHERE:return N(((cypher_where*)n)->expr);
 case AST_NODE_RETURN_ITEM:return N(((cypher_return_item*)n)->expr);
 case AST_NODE_ORDER_BY:return N(((cypher_order_by_item*)n)->expr);
 case AST_NODE_PATH:{cypher_path*q=(void*)n;return q->type==PATH_TYPE_NORMAL&&L(q->elements);}
 case AST_NODE_NODE_PATTERN:{cypher_node_pattern*q=(void*)n;return L(q->labels)&&L(q->label_alternatives)&&N(q->properties);}
 case AST_NODE_REL_PATTERN:{cypher_rel_pattern*q=(void*)n;return !q->varlen&&L(q->types)&&N(q->properties);}
 case AST_NODE_LITERAL:case AST_NODE_IDENTIFIER:case AST_NODE_PARAMETER:return 1;
 case AST_NODE_PROPERTY:return N(((cypher_property*)n)->expr);
 case AST_NODE_LABEL_EXPR:return N(((cypher_label_expr*)n)->expr);
 case AST_NODE_NOT_EXPR:return N(((cypher_not_expr*)n)->expr);
 case AST_NODE_NULL_CHECK:return N(((cypher_null_check*)n)->expr);
 case AST_NODE_BINARY_OP:{cypher_binary_op*q=(void*)n;return N(q->left)&&N(q->right);}
 case AST_NODE_FUNCTION_CALL:{cypher_function_call*q=(void*)n;const char*safe[]={"count","sum","avg","min","max","collect","labels","type","properties","coalesce","size","toString","toInteger","toFloat","toBoolean","head","last",NULL};for(int i=0;safe[i];i++)if(!strcasecmp(q->function_name,safe[i]))return L(q->args);return 0;}
 case AST_NODE_MAP:return L(((cypher_map*)n)->pairs);
 case AST_NODE_MAP_PAIR:return strcmp(((cypher_map_pair*)n)->key,"__cuttle_entity_kind")&&N(((cypher_map_pair*)n)->value);
 case AST_NODE_LIST:return L(((cypher_list*)n)->items);
 default:return 0; // Includes every mutation, procedure, nested subquery, file loader, and unknown node.
 }}
int cuttle_policy(const char*q){cypher_parse_result*r=parse_cypher_query_ext(q);if(!r)return 1;int rc=r->ast?(node(r->ast,0)?0:2):1;cypher_parse_result_free(r);return rc;}
